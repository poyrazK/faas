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
	// MetricDefinition holds a funnel or retention metric, Cuped whether
	// CUPED is on, and MinCupedPValues the running minimum adjusted
	// p-values (ADR 20261119102741).
	MetricDefinition flags.MetricDefinition
	Cuped            bool
	MinCupedPValues  map[string]float64
	// Unit is customer or subject; Layer and Traffic the layer it joins and
	// the share it asks for, LayerFrom and LayerTo the slice it holds while
	// running; Allocation fixed or bandit, with the bandit's last posterior
	// and re-weight time; DenominatorEvent a ratio metric's denominator and
	// Percentile a percentile metric's percentile (ADR 20261119173702).
	Unit               string
	Layer              string
	Traffic            int
	LayerFrom, LayerTo int
	Allocation         string
	Bandit             []flags.BanditArm
	BanditWeightedAt   *time.Time
	DenominatorEvent   string
	Percentile         int
	// NotifyChannelIDs are the notification channels the experiment's
	// decisions and problems go to; empty uses the environment's
	// (ADR-20261125085913).
	NotifyChannelIDs []string
}

// SubjectUnits reports whether the experiment counts subjects; its units
// come from SDK exposures and its metrics from product analytics.
func (x FlagExperiment) SubjectUnits() bool { return x.Unit == flags.ExperimentUnitSubject }

// UsesAnalyticsConversions reports whether the reconciler marks the
// experiment's conversions from analytics events.
func (x FlagExperiment) UsesAnalyticsConversions() bool {
	return flags.AnalyticsMetric(x.MetricKind) || x.SubjectUnits()
}

// FlagExperimentUnitRef is one unit awaiting a funnel or retention check.
// CustomerID is the unit key; for a subject unit Customer and SubjectHash
// are its customer and subject.
type FlagExperimentUnitRef struct {
	CustomerID     string
	FirstExposedAt time.Time
	Customer       string
	SubjectHash    []byte
}

// FlagExperimentAnalyticsStore keeps funnel and retention conversions and
// CUPED values (ADR 20261119102741 §1–2).
type FlagExperimentAnalyticsStore interface {
	// FlagExperimentConversionCandidates lists unconverted units whose
	// window opened by now and whose deadline is after notBefore, in
	// customer order after afterCustomer.
	FlagExperimentConversionCandidates(ctx context.Context, x FlagExperiment, afterCustomer string, now, notBefore time.Time, limit int) ([]FlagExperimentUnitRef, error)
	// MarkFlagExperimentUnitConversions sets converted_at, by customer.
	MarkFlagExperimentUnitConversions(ctx context.Context, experimentID string, conversions map[string]time.Time) error
	// FlagExperimentUnitsWithoutPreValue lists units without a CUPED value.
	FlagExperimentUnitsWithoutPreValue(ctx context.Context, experimentID string, limit int) ([]FlagExperimentUnitRef, error)
	// SetFlagExperimentPreValues sets each listed unit's CUPED value.
	SetFlagExperimentPreValues(ctx context.Context, experimentID string, values map[string]float64) error
	// FlagExperimentCupedArms returns per-variant CUPED sums over the
	// counted units and how many counted units still lack a value.
	FlagExperimentCupedArms(ctx context.Context, x FlagExperiment, minUnits int64) ([]flags.CupedArm, int64, error)
}

// FlagExperimentStore keeps experiments and their per-customer units.
// FlagExperimentStartCursor is a started experiment's first telemetry
// cursor: just before the start's minute. Gateways collapse request telemetry
// into rows stamped at the start of their minute
// (FAAS_REQUEST_TELEMETRY_MINUTE_COLLAPSE), and the reconciler reads rows
// after the cursor, so a cursor at the start instant would skip every exposure
// in the start minute. The few requests of that minute before the start count
// too: they were served by the same weighted rule.
func FlagExperimentStartCursor(start time.Time) time.Time {
	return start.UTC().Truncate(time.Minute).Add(-time.Microsecond)
}

type FlagExperimentStore interface {
	CreateFlagExperiment(ctx context.Context, x FlagExperiment, maxExperiments int) (FlagExperiment, error)
	GetFlagExperiment(ctx context.Context, environmentID, key string) (FlagExperiment, error)
	ListFlagExperiments(ctx context.Context, environmentID string) ([]FlagExperiment, error)
	// StartFlagExperiment starts a draft with its weights and the layer
	// slice it holds ([0, 0] without a layer).
	StartFlagExperiment(ctx context.Context, environmentID, key string, weights map[string]int, slice [2]int, now time.Time, maxRunning int) (FlagExperiment, error)
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
	SaveFlagExperimentProgress(ctx context.Context, experimentID string, cursor time.Time, minPValues, minCupedPValues map[string]float64) error
}

func flagExperimentFromRow(r sqlc.FlagExperiment) FlagExperiment {
	x := FlagExperiment{
		ID: pgUUIDString(r.ID), AccountID: pgUUIDString(r.AccountID), ProjectID: pgUUIDString(r.ProjectID), EnvironmentID: pgUUIDString(r.EnvironmentID),
		Key: r.Key, FlagKey: r.FlagKey, RuleID: r.RuleID, Control: r.Control, MetricEvent: r.MetricEvent, MetricKind: r.MetricKind,
		Status: r.Status, MixingLift: r.MixingLift, CreatedBy: r.CreatedBy, CreatedAt: r.CreatedAt.Time,
		Weights: map[string]int{}, MinPValues: map[string]float64{}, MinCupedPValues: map[string]float64{}, Cuped: r.Cuped,
	}
	_ = json.Unmarshal(r.Weights, &x.Weights)
	_ = json.Unmarshal(r.MinPValues, &x.MinPValues)
	_ = json.Unmarshal(r.MinCupedPValues, &x.MinCupedPValues)
	_ = json.Unmarshal(r.MetricDefinition, &x.MetricDefinition)
	_ = json.Unmarshal(r.Guardrails, &x.Guardrails)
	if len(r.Breach) > 0 {
		_ = json.Unmarshal(r.Breach, &x.Breach)
	}
	x.StopReason = r.StopReason
	x.Unit, x.Layer, x.Traffic, x.LayerFrom, x.LayerTo = r.Unit, r.Layer, int(r.Traffic), int(r.LayerFrom), int(r.LayerTo)
	x.Allocation, x.DenominatorEvent, x.Percentile = r.Allocation, r.DenominatorEvent, int(r.Percentile)
	x.NotifyChannelIDs = pgUUIDStrings(r.NotifyChannelIds)
	_ = json.Unmarshal(r.Bandit, &x.Bandit)
	if r.BanditWeightedAt.Valid {
		t := r.BanditWeightedAt.Time
		x.BanditWeightedAt = &t
	}
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
	definition, err := json.Marshal(x.MetricDefinition)
	if err != nil {
		return FlagExperiment{}, fmt.Errorf("flag experiment create: %w", err)
	}
	row, err := sqlc.New().CreateFlagExperiment(ctx, s.pool, sqlc.CreateFlagExperimentParams{
		ID: uuidToPgtype(uuid.NewString()), AccountID: uuidToPgtype(x.AccountID), ProjectID: uuidToPgtype(x.ProjectID),
		EnvironmentID: uuidToPgtype(x.EnvironmentID), Key: x.Key, FlagKey: x.FlagKey, RuleID: x.RuleID, Control: x.Control,
		MetricEvent: x.MetricEvent, MetricKind: kind, MixingLift: x.MixingLift, Weights: weights, Guardrails: guardrails,
		CreatedBy: x.CreatedBy, MaxExperiments: int64(maxExperiments), MetricDefinition: definition, Cuped: x.Cuped,
		Unit: orDefault(x.Unit, flags.ExperimentUnitCustomer), Layer: x.Layer, Traffic: int32(x.Traffic),
		Allocation: orDefault(x.Allocation, flags.ExperimentAllocationFixed), DenominatorEvent: x.DenominatorEvent, Percentile: int32(x.Percentile),
		NotifyChannelIds: pgUUIDList(x.NotifyChannelIDs),
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
func (s *PgStore) StartFlagExperiment(ctx context.Context, environmentID, key string, weights map[string]int, slice [2]int, now time.Time, maxRunning int) (FlagExperiment, error) {
	raw, err := jsonObject(weights)
	if err != nil {
		return FlagExperiment{}, fmt.Errorf("flag experiment start: %w", err)
	}
	row, err := sqlc.New().StartFlagExperiment(ctx, s.pool, sqlc.StartFlagExperimentParams{
		EnvironmentID: uuidToPgtype(environmentID), Key: key, Now: pgTimestamptzUTC(now), Weights: raw, MaxRunning: int64(maxRunning),
		LayerFrom: int32(slice[0]), LayerTo: int32(slice[1])})
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
	if flags.AnalyticsMetric(x.MetricKind) {
		rows, err := q.FlagExperimentHorizonArms(ctx, s.pool, sqlc.FlagExperimentHorizonArmsParams{ExperimentID: id,
			HorizonSeconds: x.MetricDefinition.Horizon(x.MetricKind).Seconds(), Until: pgTimestamptzUTC(x.TelemetryCursor)})
		if err != nil {
			return nil, 0, flagExperimentErr("arms", err)
		}
		arms := make([]flags.Arm, 0, len(rows))
		for _, r := range rows {
			arms = append(arms, flags.Arm{Variant: r.Variant, Exposed: r.Exposed, Converted: r.Converted})
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
		if x.MetricKind == flags.ExperimentMetricRatio {
			if _, err := q.FoldFlagExperimentDenominators(ctx, tx, sqlc.FoldFlagExperimentDenominatorsParams{
				ExperimentID: uuidToPgtype(x.ID), EnvironmentID: uuidToPgtype(x.EnvironmentID), EventName: x.DenominatorEvent,
				FromDay: pgtype.Date{Time: from, Valid: true}, ThroughDay: pgtype.Date{Time: through, Valid: true}}); err != nil {
				return fmt.Errorf("flag experiment fold denominators: %w", err)
			}
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
			ValueThroughDay: r.ValueThroughDay, MetricDefinition: r.MetricDefinition, Cuped: r.Cuped, MinCupedPValues: r.MinCupedPValues,
			Unit: r.Unit, Layer: r.Layer, Traffic: r.Traffic, LayerFrom: r.LayerFrom, LayerTo: r.LayerTo, Allocation: r.Allocation,
			Bandit: r.Bandit, BanditWeightedAt: r.BanditWeightedAt, DenominatorEvent: r.DenominatorEvent, Percentile: r.Percentile,
			NotifyChannelIds: r.NotifyChannelIds,
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
		if flags.AnalyticsMetric(x.MetricKind) {
			// Funnel and retention conversions come from analytics
			// events, marked by the reconciler (ADR 20261119102741 §1).
			return nil
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
func (s *PgStore) SaveFlagExperimentProgress(ctx context.Context, experimentID string, cursor time.Time, minPValues, minCupedPValues map[string]float64) error {
	raw, err := jsonObject(minPValues)
	if err != nil {
		return fmt.Errorf("flag experiment progress: %w", err)
	}
	rawCuped, err := jsonObject(minCupedPValues)
	if err != nil {
		return fmt.Errorf("flag experiment progress: %w", err)
	}
	if err := sqlc.New().AdvanceFlagExperiment(ctx, s.pool, sqlc.AdvanceFlagExperimentParams{
		ID: uuidToPgtype(experimentID), TelemetryCursor: pgTimestamptzUTC(cursor), MinPValues: raw, MinCupedPValues: rawCuped}); err != nil {
		return fmt.Errorf("flag experiment progress: %w", err)
	}
	return nil
}

// FlagExperimentConversionCandidates implements FlagExperimentAnalyticsStore.
func (s *PgStore) FlagExperimentConversionCandidates(ctx context.Context, x FlagExperiment, afterCustomer string, now, notBefore time.Time, limit int) ([]FlagExperimentUnitRef, error) {
	if afterCustomer == "" {
		afterCustomer = uuid.Nil.String()
	}
	rows, err := sqlc.New().FlagExperimentConversionCandidates(ctx, s.pool, sqlc.FlagExperimentConversionCandidatesParams{
		ExperimentID: uuidToPgtype(x.ID), AfterCustomer: uuidToPgtype(afterCustomer),
		StartSeconds: x.MetricDefinition.StartOffset(x.MetricKind).Seconds(), Now: pgTimestamptzUTC(now),
		HorizonSeconds: conversionHorizon(x).Seconds(), NotBefore: pgTimestamptzUTC(notBefore), MaxRows: int32(limit)})
	if err != nil {
		return nil, flagExperimentErr("conversion candidates", err)
	}
	out := make([]FlagExperimentUnitRef, 0, len(rows))
	for _, r := range rows {
		out = append(out, unitRef(r.CustomerID, r.UnitCustomerID, r.SubjectHash, r.FirstExposedAt.Time))
	}
	return out, nil
}

// MarkFlagExperimentUnitConversions implements FlagExperimentAnalyticsStore.
func (s *PgStore) MarkFlagExperimentUnitConversions(ctx context.Context, experimentID string, conversions map[string]time.Time) error {
	if len(conversions) == 0 {
		return nil
	}
	ids := make([]pgtype.UUID, 0, len(conversions))
	ats := make([]pgtype.Timestamptz, 0, len(conversions))
	for id, at := range conversions {
		ids = append(ids, uuidToPgtype(id))
		ats = append(ats, pgTimestamptzUTC(at))
	}
	if _, err := sqlc.New().MarkFlagExperimentUnitConversions(ctx, s.pool, sqlc.MarkFlagExperimentUnitConversionsParams{
		ExperimentID: uuidToPgtype(experimentID), CustomerIds: ids, Ats: ats}); err != nil {
		return fmt.Errorf("flag experiment mark conversions: %w", err)
	}
	return nil
}

// FlagExperimentUnitsWithoutPreValue implements FlagExperimentAnalyticsStore.
func (s *PgStore) FlagExperimentUnitsWithoutPreValue(ctx context.Context, experimentID string, limit int) ([]FlagExperimentUnitRef, error) {
	rows, err := sqlc.New().FlagExperimentUnitsWithoutPreValue(ctx, s.pool, sqlc.FlagExperimentUnitsWithoutPreValueParams{
		ExperimentID: uuidToPgtype(experimentID), MaxRows: int32(limit)})
	if err != nil {
		return nil, flagExperimentErr("units without pre value", err)
	}
	out := make([]FlagExperimentUnitRef, 0, len(rows))
	for _, r := range rows {
		out = append(out, unitRef(r.CustomerID, r.UnitCustomerID, r.SubjectHash, time.Time{}))
	}
	return out, nil
}

// SetFlagExperimentPreValues implements FlagExperimentAnalyticsStore.
func (s *PgStore) SetFlagExperimentPreValues(ctx context.Context, experimentID string, values map[string]float64) error {
	if len(values) == 0 {
		return nil
	}
	ids := make([]pgtype.UUID, 0, len(values))
	vals := make([]float64, 0, len(values))
	for id, v := range values {
		ids = append(ids, uuidToPgtype(id))
		vals = append(vals, v)
	}
	if _, err := sqlc.New().SetFlagExperimentPreValues(ctx, s.pool, sqlc.SetFlagExperimentPreValuesParams{
		ExperimentID: uuidToPgtype(experimentID), CustomerIds: ids, PreValues: vals}); err != nil {
		return fmt.Errorf("flag experiment pre values: %w", err)
	}
	return nil
}

// FlagExperimentCupedArms implements FlagExperimentAnalyticsStore.
func (s *PgStore) FlagExperimentCupedArms(ctx context.Context, x FlagExperiment, minUnits int64) ([]flags.CupedArm, int64, error) {
	q := sqlc.New()
	var arms []flags.CupedArm
	var pending int64
	if x.MetricKind == flags.ExperimentMetricMean {
		rows, err := q.FlagExperimentMeanCupedArms(ctx, s.pool, sqlc.FlagExperimentMeanCupedArmsParams{
			EnvironmentID: uuidToPgtype(x.EnvironmentID), EventName: x.MetricEvent,
			OpenFromDay: pgtype.Date{Time: flagExperimentOpenFromDay(x), Valid: true}, ExperimentID: uuidToPgtype(x.ID), MinUnits: minUnits})
		if err != nil {
			return nil, 0, flagExperimentErr("cuped arms", err)
		}
		for _, r := range rows {
			arms = append(arms, flags.CupedArm{Variant: r.Variant, N: r.N, SumY: r.SumY, SumY2: r.SumY2, SumX: r.SumX, SumX2: r.SumX2, SumXY: r.SumXy})
			pending += r.Pending
		}
		return arms, pending, nil
	}
	until := x.TelemetryCursor
	horizon := x.MetricDefinition.Horizon(x.MetricKind)
	if horizon == 0 {
		// Conversion counts every unit: no deadline.
		until = time.Date(9999, 1, 1, 0, 0, 0, 0, time.UTC)
	}
	rows, err := q.FlagExperimentBinaryCupedArms(ctx, s.pool, sqlc.FlagExperimentBinaryCupedArmsParams{
		ExperimentID: uuidToPgtype(x.ID), HorizonSeconds: horizon.Seconds(), Until: pgTimestamptzUTC(until)})
	if err != nil {
		return nil, 0, flagExperimentErr("cuped arms", err)
	}
	for _, r := range rows {
		// Y is 0 or 1, so the sum of squares is the sum.
		arms = append(arms, flags.CupedArm{Variant: r.Variant, N: r.N, SumY: r.SumY, SumY2: r.SumY, SumX: r.SumX, SumX2: r.SumX2, SumXY: r.SumXy})
		pending += r.Pending
	}
	return arms, pending, nil
}
