package state

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/onebox-faas/faas/pkg/flags"
	"github.com/onebox-faas/faas/pkg/state/sqlc"
)

// ErrFlagExperimentLimit is returned when an environment already holds the
// maximum number of experiments, or of running ones.
var ErrFlagExperimentLimit = errors.New("state: flag experiment limit reached")

// FlagExperiment is one experiment on a variant flag's weighted rule
// (ADR 20261115163113).
type FlagExperiment struct {
	ID, AccountID, ProjectID, EnvironmentID string
	Key, FlagKey, RuleID, Control           string
	MetricEvent, MetricKind, Status         string
	MixingLift                              float64
	Weights                                 map[string]int
	MinPValues                              map[string]float64
	TelemetryCursor                         time.Time
	CreatedBy                               string
	CreatedAt                               time.Time
	StartedAt, StoppedAt                    *time.Time
	// EnvironmentSlug and Plan are set on running experiments listed for the
	// reconciler.
	EnvironmentSlug, Plan string
}

// FlagExperimentStore keeps experiments and their per-customer units.
type FlagExperimentStore interface {
	CreateFlagExperiment(ctx context.Context, x FlagExperiment, maxExperiments int) (FlagExperiment, error)
	GetFlagExperiment(ctx context.Context, environmentID, key string) (FlagExperiment, error)
	ListFlagExperiments(ctx context.Context, environmentID string) ([]FlagExperiment, error)
	StartFlagExperiment(ctx context.Context, environmentID, key string, weights map[string]int, now time.Time, maxRunning int) (FlagExperiment, error)
	StopFlagExperiment(ctx context.Context, environmentID, key string, now time.Time) (FlagExperiment, error)
	DeleteFlagExperiment(ctx context.Context, environmentID, key string) (bool, error)
	// FlagExperimentArms returns exposed and converted units per variant,
	// leaving out crossed units, and how many units crossed.
	FlagExperimentArms(ctx context.Context, experimentID string) ([]flags.Arm, int64, error)
	ListRunningFlagExperiments(ctx context.Context, afterID string, limit int) ([]FlagExperiment, error)
	// AdvanceFlagExperimentUnits adds units first exposed in request
	// telemetry received in (from, until] and marks conversions; it returns
	// the number of exposure rows read.
	AdvanceFlagExperimentUnits(ctx context.Context, x FlagExperiment, from, until time.Time, maxRows int) (int, error)
	SaveFlagExperimentProgress(ctx context.Context, experimentID string, cursor time.Time, minPValues map[string]float64) error
}

func flagExperimentFromRow(r sqlc.FlagExperiment) FlagExperiment {
	x := FlagExperiment{
		ID: pgUUIDString(r.ID), AccountID: pgUUIDString(r.AccountID), ProjectID: pgUUIDString(r.ProjectID), EnvironmentID: pgUUIDString(r.EnvironmentID),
		Key: r.Key, FlagKey: r.FlagKey, RuleID: r.RuleID, Control: r.Control, MetricEvent: r.MetricEvent, MetricKind: r.MetricKind,
		Status: r.Status, MixingLift: r.MixingLift, CreatedBy: r.CreatedBy, CreatedAt: r.CreatedAt.Time,
		Weights: map[string]int{}, MinPValues: map[string]float64{},
	}
	_ = json.Unmarshal(r.Weights, &x.Weights)
	_ = json.Unmarshal(r.MinPValues, &x.MinPValues)
	if r.TelemetryCursor.Valid {
		x.TelemetryCursor = r.TelemetryCursor.Time
	}
	if r.StartedAt.Valid {
		t := r.StartedAt.Time
		x.StartedAt = &t
	}
	if r.StoppedAt.Valid {
		t := r.StoppedAt.Time
		x.StoppedAt = &t
	}
	return x
}

func flagExperimentErr(op string, err error) error {
	switch {
	case errors.Is(err, pgx.ErrNoRows):
		return ErrNotFound
	case isUniqueViolation(err):
		return ErrConflict
	default:
		return fmt.Errorf("flag experiment %s: %w", op, err)
	}
}

// CreateFlagExperiment implements FlagExperimentStore. A duplicate key is
// ErrConflict; an environment at maxExperiments is ErrFlagExperimentLimit.
func (s *PgStore) CreateFlagExperiment(ctx context.Context, x FlagExperiment, maxExperiments int) (FlagExperiment, error) {
	weights, err := jsonObject(x.Weights)
	if err != nil {
		return FlagExperiment{}, fmt.Errorf("flag experiment create: %w", err)
	}
	row, err := sqlc.New().CreateFlagExperiment(ctx, s.pool, sqlc.CreateFlagExperimentParams{
		ID: uuidToPgtype(uuid.NewString()), AccountID: uuidToPgtype(x.AccountID), ProjectID: uuidToPgtype(x.ProjectID),
		EnvironmentID: uuidToPgtype(x.EnvironmentID), Key: x.Key, FlagKey: x.FlagKey, RuleID: x.RuleID, Control: x.Control,
		MetricEvent: x.MetricEvent, MixingLift: x.MixingLift, Weights: weights, CreatedBy: x.CreatedBy, MaxExperiments: int64(maxExperiments),
	})
	if errors.Is(err, pgx.ErrNoRows) {
		return FlagExperiment{}, ErrFlagExperimentLimit
	}
	if err != nil {
		return FlagExperiment{}, flagExperimentErr("create", err)
	}
	return flagExperimentFromRow(row), nil
}

// GetFlagExperiment implements FlagExperimentStore.
func (s *PgStore) GetFlagExperiment(ctx context.Context, environmentID, key string) (FlagExperiment, error) {
	row, err := sqlc.New().GetFlagExperiment(ctx, s.pool, sqlc.GetFlagExperimentParams{EnvironmentID: uuidToPgtype(environmentID), Key: key})
	if err != nil {
		return FlagExperiment{}, flagExperimentErr("get", err)
	}
	return flagExperimentFromRow(row), nil
}

// ListFlagExperiments implements FlagExperimentStore.
func (s *PgStore) ListFlagExperiments(ctx context.Context, environmentID string) ([]FlagExperiment, error) {
	rows, err := sqlc.New().ListFlagExperiments(ctx, s.pool, uuidToPgtype(environmentID))
	if err != nil {
		return nil, flagExperimentErr("list", err)
	}
	out := make([]FlagExperiment, 0, len(rows))
	for _, r := range rows {
		out = append(out, flagExperimentFromRow(r))
	}
	return out, nil
}

// StartFlagExperiment implements FlagExperimentStore. It is ErrNotFound when
// no draft has the key, ErrFlagExperimentLimit at maxRunning, and ErrConflict
// when another experiment already runs on the same rule.
func (s *PgStore) StartFlagExperiment(ctx context.Context, environmentID, key string, weights map[string]int, now time.Time, maxRunning int) (FlagExperiment, error) {
	raw, err := jsonObject(weights)
	if err != nil {
		return FlagExperiment{}, fmt.Errorf("flag experiment start: %w", err)
	}
	row, err := sqlc.New().StartFlagExperiment(ctx, s.pool, sqlc.StartFlagExperimentParams{
		EnvironmentID: uuidToPgtype(environmentID), Key: key, Now: pgTimestamptzUTC(now), Weights: raw, MaxRunning: int64(maxRunning)})
	if errors.Is(err, pgx.ErrNoRows) {
		if _, getErr := s.GetFlagExperiment(ctx, environmentID, key); getErr != nil {
			return FlagExperiment{}, getErr
		}
		return FlagExperiment{}, ErrFlagExperimentLimit
	}
	if err != nil {
		return FlagExperiment{}, flagExperimentErr("start", err)
	}
	return flagExperimentFromRow(row), nil
}

// StopFlagExperiment implements FlagExperimentStore; ErrNotFound when no
// running experiment has the key.
func (s *PgStore) StopFlagExperiment(ctx context.Context, environmentID, key string, now time.Time) (FlagExperiment, error) {
	row, err := sqlc.New().StopFlagExperiment(ctx, s.pool, sqlc.StopFlagExperimentParams{EnvironmentID: uuidToPgtype(environmentID), Key: key, Now: pgTimestamptzUTC(now)})
	if err != nil {
		return FlagExperiment{}, flagExperimentErr("stop", err)
	}
	return flagExperimentFromRow(row), nil
}

// DeleteFlagExperiment implements FlagExperimentStore. A running experiment
// is not deleted.
func (s *PgStore) DeleteFlagExperiment(ctx context.Context, environmentID, key string) (bool, error) {
	n, err := sqlc.New().DeleteFlagExperiment(ctx, s.pool, sqlc.DeleteFlagExperimentParams{EnvironmentID: uuidToPgtype(environmentID), Key: key})
	if err != nil {
		return false, flagExperimentErr("delete", err)
	}
	return n > 0, nil
}

// FlagExperimentArms implements FlagExperimentStore.
func (s *PgStore) FlagExperimentArms(ctx context.Context, experimentID string) ([]flags.Arm, int64, error) {
	id := uuidToPgtype(experimentID)
	q := sqlc.New()
	rows, err := q.FlagExperimentArms(ctx, s.pool, id)
	if err != nil {
		return nil, 0, flagExperimentErr("arms", err)
	}
	crossed, err := q.FlagExperimentCrossedUnits(ctx, s.pool, id)
	if err != nil {
		return nil, 0, flagExperimentErr("arms", err)
	}
	arms := make([]flags.Arm, 0, len(rows))
	for _, r := range rows {
		arms = append(arms, flags.Arm{Variant: r.Variant, Exposed: r.Exposed, Converted: r.Converted})
	}
	return arms, crossed, nil
}

// ListRunningFlagExperiments implements FlagExperimentStore.
func (s *PgStore) ListRunningFlagExperiments(ctx context.Context, afterID string, limit int) ([]FlagExperiment, error) {
	if afterID == "" {
		afterID = uuid.Nil.String()
	}
	rows, err := sqlc.New().ListRunningFlagExperiments(ctx, s.pool, sqlc.ListRunningFlagExperimentsParams{AfterID: uuidToPgtype(afterID), MaxRows: int32(limit)})
	if err != nil {
		return nil, flagExperimentErr("list running", err)
	}
	out := make([]FlagExperiment, 0, len(rows))
	for _, r := range rows {
		x := flagExperimentFromRow(sqlc.FlagExperiment{
			ID: r.ID, AccountID: r.AccountID, ProjectID: r.ProjectID, EnvironmentID: r.EnvironmentID, Key: r.Key, FlagKey: r.FlagKey,
			RuleID: r.RuleID, Control: r.Control, MetricEvent: r.MetricEvent, MetricKind: r.MetricKind, Status: r.Status,
			MixingLift: r.MixingLift, Weights: r.Weights, MinPValues: r.MinPValues, TelemetryCursor: r.TelemetryCursor,
			CreatedBy: r.CreatedBy, CreatedAt: r.CreatedAt, StartedAt: r.StartedAt, StoppedAt: r.StoppedAt,
		})
		x.EnvironmentSlug, x.Plan = r.EnvironmentSlug, r.Plan
		out = append(out, x)
	}
	return out, nil
}

// AdvanceFlagExperimentUnits implements FlagExperimentStore.
func (s *PgStore) AdvanceFlagExperimentUnits(ctx context.Context, x FlagExperiment, from, until time.Time, maxRows int) (int, error) {
	var read int
	err := pgx.BeginFunc(ctx, s.pool, func(tx pgx.Tx) error {
		q := sqlc.New()
		rows, err := q.FlagExperimentExposures(ctx, tx, sqlc.FlagExperimentExposuresParams{
			AccountID: uuidToPgtype(x.AccountID), ProjectID: uuidToPgtype(x.ProjectID), EnvironmentSlug: x.EnvironmentSlug,
			ReceivedFrom: pgTimestamptzUTC(from), ReceivedUntil: pgTimestamptzUTC(until), FlagKey: x.FlagKey, RuleID: x.RuleID, MaxRows: int32(maxRows),
		})
		if err != nil {
			return fmt.Errorf("flag experiment exposures: %w", err)
		}
		read = len(rows)
		if err := addFlagExperimentUnits(ctx, q, tx, x.ID, rows); err != nil {
			return err
		}
		since := from
		if x.StartedAt != nil {
			since = *x.StartedAt
		}
		_, err = q.MarkFlagExperimentConversions(ctx, tx, sqlc.MarkFlagExperimentConversionsParams{
			ExperimentID: uuidToPgtype(x.ID), EnvironmentID: uuidToPgtype(x.EnvironmentID), EventName: x.MetricEvent,
			SinceDay: pgtype.Date{Time: time.Date(since.UTC().Year(), since.UTC().Month(), since.UTC().Day(), 0, 0, 0, 0, time.UTC), Valid: true},
		})
		if err != nil {
			return fmt.Errorf("flag experiment conversions: %w", err)
		}
		return nil
	})
	return read, err
}

// addFlagExperimentUnits folds exposure rows to one row per customer: a
// customer seen with two variants in the window is crossed.
func addFlagExperimentUnits(ctx context.Context, q *sqlc.Queries, tx pgx.Tx, experimentID string, rows []sqlc.FlagExperimentExposuresRow) error {
	type unit struct {
		variant string
		first   time.Time
		crossed bool
		id      pgtype.UUID
	}
	units := map[string]*unit{}
	order := []string{}
	for _, r := range rows {
		key := pgUUIDString(r.CustomerID)
		u, ok := units[key]
		if !ok {
			units[key] = &unit{variant: r.Variant, first: r.FirstAt.Time, id: r.CustomerID}
			order = append(order, key)
			continue
		}
		if u.variant != r.Variant {
			u.crossed = true
		}
		if r.FirstAt.Time.Before(u.first) {
			u.first, u.variant = r.FirstAt.Time, r.Variant
		}
	}
	if len(order) == 0 {
		return nil
	}
	p := sqlc.AddFlagExperimentUnitsParams{ExperimentID: uuidToPgtype(experimentID)}
	for _, key := range order {
		u := units[key]
		p.CustomerIds = append(p.CustomerIds, u.id)
		p.Variants = append(p.Variants, u.variant)
		p.FirstAts = append(p.FirstAts, pgTimestamptzUTC(u.first))
		p.Crossed = append(p.Crossed, u.crossed)
	}
	if err := q.AddFlagExperimentUnits(ctx, tx, p); err != nil {
		return fmt.Errorf("flag experiment units: %w", err)
	}
	return nil
}

// jsonObject encodes a map as a JSON object; a nil map is {} so the jsonb
// object CHECKs hold.
func jsonObject[V any](m map[string]V) ([]byte, error) {
	if m == nil {
		return []byte("{}"), nil
	}
	return json.Marshal(m)
}

// SaveFlagExperimentProgress implements FlagExperimentStore.
func (s *PgStore) SaveFlagExperimentProgress(ctx context.Context, experimentID string, cursor time.Time, minPValues map[string]float64) error {
	raw, err := jsonObject(minPValues)
	if err != nil {
		return fmt.Errorf("flag experiment progress: %w", err)
	}
	if err := sqlc.New().AdvanceFlagExperiment(ctx, s.pool, sqlc.AdvanceFlagExperimentParams{
		ID: uuidToPgtype(experimentID), TelemetryCursor: pgTimestamptzUTC(cursor), MinPValues: raw}); err != nil {
		return fmt.Errorf("flag experiment progress: %w", err)
	}
	return nil
}
