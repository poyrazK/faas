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
	// Guardrails, the fixed latency threshold, why and how it stopped, and
	// the last UTC day folded into unit values (ADR 20261116190208).
	Guardrails         flags.GuardrailSettings
	LatencyThresholdMS *int
	StopReason         string
	Breach             map[string]any
	ValueThroughDay    *time.Time
}

// FlagExperimentStore keeps experiments and their per-customer units.
type FlagExperimentStore interface {
	CreateFlagExperiment(ctx context.Context, x FlagExperiment, maxExperiments int) (FlagExperiment, error)
	GetFlagExperiment(ctx context.Context, environmentID, key string) (FlagExperiment, error)
	ListFlagExperiments(ctx context.Context, environmentID string) ([]FlagExperiment, error)
	StartFlagExperiment(ctx context.Context, environmentID, key string, weights map[string]int, now time.Time, maxRunning int) (FlagExperiment, error)
	// StopFlagExperiment stops a running experiment, recording why and, for
	// a guardrail, the breach.
	StopFlagExperiment(ctx context.Context, environmentID, key string, now time.Time, reason string, breach map[string]any) (FlagExperiment, error)
	DeleteFlagExperiment(ctx context.Context, environmentID, key string) (bool, error)
	// FlagExperimentArms returns exposed and converted units per variant,
	// leaving out crossed units, and how many units crossed. For mean
	// metrics an arm's Sum and SumSq are its units' values, clamped to the
	// 1st and 99th percentile once there are minUnits units.
	FlagExperimentArms(ctx context.Context, x FlagExperiment, minUnits int64) ([]flags.Arm, int64, error)
	// FlagExperimentGuardrailArms returns per-variant server error and slow
	// request sums with customers as units.
	FlagExperimentGuardrailArms(ctx context.Context, experimentID string) (errs, slow []flags.RatioArm, err error)
	// FlagExperimentLatency returns each variant's latency histogram.
	FlagExperimentLatency(ctx context.Context, experimentID string) (map[string]map[int]int64, error)
	// SetFlagExperimentLatencyThreshold fixes a running experiment's latency
	// threshold once.
	SetFlagExperimentLatencyThreshold(ctx context.Context, experimentID string, ms int) error
	// FoldFlagExperimentValues adds metric values of days after the
	// experiment's value_through_day through through to its units.
	FoldFlagExperimentValues(ctx context.Context, x FlagExperiment, through time.Time) error
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
	_ = json.Unmarshal(r.Guardrails, &x.Guardrails)
	if len(r.Breach) > 0 {
		_ = json.Unmarshal(r.Breach, &x.Breach)
	}
	x.StopReason = r.StopReason
	if r.LatencyThresholdMs.Valid {
		ms := int(r.LatencyThresholdMs.Int32)
		x.LatencyThresholdMS = &ms
	}
	if r.ValueThroughDay.Valid {
		d := r.ValueThroughDay.Time
		x.ValueThroughDay = &d
	}
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
	guardrails, err := json.Marshal(x.Guardrails)
	if err != nil {
		return FlagExperiment{}, fmt.Errorf("flag experiment create: %w", err)
	}
	kind := x.MetricKind
	if kind == "" {
		kind = flags.ExperimentMetricConversion
	}
	row, err := sqlc.New().CreateFlagExperiment(ctx, s.pool, sqlc.CreateFlagExperimentParams{
		ID: uuidToPgtype(uuid.NewString()), AccountID: uuidToPgtype(x.AccountID), ProjectID: uuidToPgtype(x.ProjectID),
		EnvironmentID: uuidToPgtype(x.EnvironmentID), Key: x.Key, FlagKey: x.FlagKey, RuleID: x.RuleID, Control: x.Control,
		MetricEvent: x.MetricEvent, MetricKind: kind, MixingLift: x.MixingLift, Weights: weights, Guardrails: guardrails,
		CreatedBy: x.CreatedBy, MaxExperiments: int64(maxExperiments),
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
func (s *PgStore) StopFlagExperiment(ctx context.Context, environmentID, key string, now time.Time, reason string, breach map[string]any) (FlagExperiment, error) {
	var raw []byte
	if breach != nil {
		b, err := json.Marshal(breach)
		if err != nil {
			return FlagExperiment{}, fmt.Errorf("flag experiment stop: %w", err)
		}
		raw = b
	}
	row, err := sqlc.New().StopFlagExperiment(ctx, s.pool, sqlc.StopFlagExperimentParams{EnvironmentID: uuidToPgtype(environmentID), Key: key,
		Now: pgTimestamptzUTC(now), StopReason: reason, Breach: raw})
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
func (s *PgStore) FlagExperimentArms(ctx context.Context, x FlagExperiment, minUnits int64) ([]flags.Arm, int64, error) {
	id := uuidToPgtype(x.ID)
	q := sqlc.New()
	crossed, err := q.FlagExperimentCrossedUnits(ctx, s.pool, id)
	if err != nil {
		return nil, 0, flagExperimentErr("arms", err)
	}
	if x.MetricKind == flags.ExperimentMetricMean {
		rows, err := q.FlagExperimentMeanArms(ctx, s.pool, sqlc.FlagExperimentMeanArmsParams{
			EnvironmentID: uuidToPgtype(x.EnvironmentID), EventName: x.MetricEvent,
			OpenFromDay: pgtype.Date{Time: flagExperimentOpenFromDay(x), Valid: true}, ExperimentID: id, MinUnits: minUnits})
		if err != nil {
			return nil, 0, flagExperimentErr("arms", err)
		}
		arms := make([]flags.Arm, 0, len(rows))
		for _, r := range rows {
			arms = append(arms, flags.Arm{Variant: r.Variant, Exposed: r.Exposed, Converted: r.Nonzero, Sum: r.Total, SumSq: r.TotalSq})
		}
		return arms, crossed, nil
	}
	rows, err := q.FlagExperimentArms(ctx, s.pool, id)
	if err != nil {
		return nil, 0, flagExperimentErr("arms", err)
	}
	arms := make([]flags.Arm, 0, len(rows))
	for _, r := range rows {
		arms = append(arms, flags.Arm{Variant: r.Variant, Exposed: r.Exposed, Converted: r.Converted})
	}
	return arms, crossed, nil
}

// flagExperimentOpenFromDay is the first day not yet folded into unit
// values: the day after value_through_day, or the start day.
func flagExperimentOpenFromDay(x FlagExperiment) time.Time {
	if x.ValueThroughDay != nil {
		return utcDay(*x.ValueThroughDay).AddDate(0, 0, 1)
	}
	if x.StartedAt != nil {
		return utcDay(*x.StartedAt)
	}
	return utcDay(x.CreatedAt)
}

// FlagExperimentGuardrailArms implements FlagExperimentStore.
func (s *PgStore) FlagExperimentGuardrailArms(ctx context.Context, experimentID string) ([]flags.RatioArm, []flags.RatioArm, error) {
	rows, err := sqlc.New().FlagExperimentGuardrailArms(ctx, s.pool, uuidToPgtype(experimentID))
	if err != nil {
		return nil, nil, flagExperimentErr("guardrail arms", err)
	}
	errs := make([]flags.RatioArm, 0, len(rows))
	slow := make([]flags.RatioArm, 0, len(rows))
	for _, r := range rows {
		errs = append(errs, flags.RatioArm{Variant: r.Variant, Units: r.Units, Num: r.Errors, Den: r.Requests,
			NumSq: r.ErrorsSq, DenSq: r.RequestsSq, NumDen: r.ErrorsRequests})
		slow = append(slow, flags.RatioArm{Variant: r.Variant, Units: r.LatencyUnits, Num: r.Slow, Den: r.LatencyRequests,
			NumSq: r.SlowSq, DenSq: r.LatencyRequestsSq, NumDen: r.SlowLatencyRequests})
	}
	return errs, slow, nil
}

// FlagExperimentLatency implements FlagExperimentStore.
func (s *PgStore) FlagExperimentLatency(ctx context.Context, experimentID string) (map[string]map[int]int64, error) {
	rows, err := sqlc.New().FlagExperimentLatency(ctx, s.pool, uuidToPgtype(experimentID))
	if err != nil {
		return nil, flagExperimentErr("latency", err)
	}
	out := map[string]map[int]int64{}
	for _, r := range rows {
		if out[r.Variant] == nil {
			out[r.Variant] = map[int]int64{}
		}
		out[r.Variant][int(r.Bucket)] += r.Requests
	}
	return out, nil
}

// SetFlagExperimentLatencyThreshold implements FlagExperimentStore.
func (s *PgStore) SetFlagExperimentLatencyThreshold(ctx context.Context, experimentID string, ms int) error {
	if err := sqlc.New().SetFlagExperimentLatencyThreshold(ctx, s.pool, sqlc.SetFlagExperimentLatencyThresholdParams{
		ID: uuidToPgtype(experimentID), LatencyThresholdMs: int32(ms)}); err != nil {
		return fmt.Errorf("flag experiment latency threshold: %w", err)
	}
	return nil
}

// FoldFlagExperimentValues implements FlagExperimentStore. The fold and the
// new value_through_day commit together, so a day is never added twice.
func (s *PgStore) FoldFlagExperimentValues(ctx context.Context, x FlagExperiment, through time.Time) error {
	from := flagExperimentOpenFromDay(x)
	through = utcDay(through)
	if through.Before(from) {
		return nil
	}
	return pgx.BeginFunc(ctx, s.pool, func(tx pgx.Tx) error {
		q := sqlc.New()
		if _, err := q.FoldFlagExperimentValues(ctx, tx, sqlc.FoldFlagExperimentValuesParams{
			ExperimentID: uuidToPgtype(x.ID), EnvironmentID: uuidToPgtype(x.EnvironmentID), EventName: x.MetricEvent,
			FromDay: pgtype.Date{Time: from, Valid: true}, ThroughDay: pgtype.Date{Time: through, Valid: true}}); err != nil {
			return fmt.Errorf("flag experiment fold values: %w", err)
		}
		if err := q.SetFlagExperimentValueThrough(ctx, tx, sqlc.SetFlagExperimentValueThroughParams{
			ID: uuidToPgtype(x.ID), ThroughDay: pgtype.Date{Time: through, Valid: true}}); err != nil {
			return fmt.Errorf("flag experiment fold values: %w", err)
		}
		return nil
	})
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
			Guardrails: r.Guardrails, LatencyThresholdMs: r.LatencyThresholdMs, StopReason: r.StopReason, Breach: r.Breach,
			ValueThroughDay: r.ValueThroughDay,
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
		threshold := int32(-1)
		if x.LatencyThresholdMS != nil {
			threshold = int32(*x.LatencyThresholdMS)
		}
		rows, err := q.FlagExperimentExposures(ctx, tx, sqlc.FlagExperimentExposuresParams{
			LatencyThresholdMs: threshold,
			AccountID:          uuidToPgtype(x.AccountID), ProjectID: uuidToPgtype(x.ProjectID), EnvironmentSlug: x.EnvironmentSlug,
			ReceivedFrom: pgTimestamptzUTC(from), ReceivedUntil: pgTimestamptzUTC(until), FlagKey: x.FlagKey, RuleID: x.RuleID, MaxRows: int32(maxRows),
		})
		if err != nil {
			return fmt.Errorf("flag experiment exposures: %w", err)
		}
		if err := q.AddFlagExperimentLatency(ctx, tx, sqlc.AddFlagExperimentLatencyParams{
			ExperimentID: uuidToPgtype(x.ID), AccountID: uuidToPgtype(x.AccountID), ProjectID: uuidToPgtype(x.ProjectID),
			EnvironmentSlug: x.EnvironmentSlug, ReceivedFrom: pgTimestamptzUTC(from), ReceivedUntil: pgTimestamptzUTC(until),
			FlagKey: x.FlagKey, RuleID: x.RuleID}); err != nil {
			return fmt.Errorf("flag experiment latency: %w", err)
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
		variant                        string
		first                          time.Time
		crossed                        bool
		id                             pgtype.UUID
		requests, errors, tested, slow int64
	}
	units := map[string]*unit{}
	order := []string{}
	for _, r := range rows {
		key := pgUUIDString(r.CustomerID)
		u, ok := units[key]
		if !ok {
			units[key] = &unit{variant: r.Variant, first: r.FirstAt.Time, id: r.CustomerID,
				requests: r.Requests, errors: r.Errors, tested: r.LatencyRequests, slow: r.SlowRequests}
			order = append(order, key)
			continue
		}
		u.requests, u.errors, u.tested, u.slow = u.requests+r.Requests, u.errors+r.Errors, u.tested+r.LatencyRequests, u.slow+r.SlowRequests
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
		p.Requests = append(p.Requests, u.requests)
		p.Errors = append(p.Errors, u.errors)
		p.LatencyRequests = append(p.LatencyRequests, u.tested)
		p.SlowRequests = append(p.SlowRequests, u.slow)
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
