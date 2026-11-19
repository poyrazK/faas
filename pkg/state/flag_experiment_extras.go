package state

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/onebox-faas/faas/pkg/flags"
	"github.com/onebox-faas/faas/pkg/state/sqlc"
)

// Subject units, ratio and percentile metrics, bandits, segments and
// metric-driven rollouts (ADR 20261119173702).

// SubjectUnitID is a subject unit's key in flag_experiment_units: a UUID
// (version 8) from the first 16 bytes of sha256("unit" NUL customer NUL
// subject hash), so the per-customer queries count it unchanged.
func SubjectUnitID(customer string, subjectHash []byte) string {
	h := sha256.New()
	h.Write([]byte("unit"))
	h.Write([]byte{0})
	h.Write([]byte(customer))
	h.Write([]byte{0})
	h.Write(subjectHash)
	var b [16]byte
	copy(b[:], h.Sum(nil))
	b[6] = b[6]&0x0f | 0x80
	b[8] = b[8]&0x3f | 0x80
	return uuid.UUID(b).String()
}

// FlagExperimentExposure is one SDK-reported subject exposure.
type FlagExperimentExposure struct {
	Flag, RuleID, Variant, CustomerID string
	SubjectHash                       []byte
	At                                time.Time
}

// RolloutMetricQuery selects a metric-driven rollout stage's groups.
type RolloutMetricQuery struct {
	AccountID, ProjectID, EnvironmentID, EnvironmentSlug string
	FlagKey, RuleID, Event                               string
	Since                                                time.Time
	MaxUnits                                             int
}

// FlagExperimentExtrasStore keeps subject units, ratio and percentile
// metric data, bandit state and segment arms.
type FlagExperimentExtrasStore interface {
	// RecordFlagExperimentExposures adds subject units for the exposures
	// of the account's active customers that match a running subject
	// experiment of the environment, and returns how many did.
	RecordFlagExperimentExposures(ctx context.Context, accountID, environmentID string, exposures []FlagExperimentExposure) (int, error)
	// FlagExperimentRatioArms returns per-variant numerator and denominator
	// sums with the delta method's squares and products.
	FlagExperimentRatioArms(ctx context.Context, x FlagExperiment) ([]flags.RatioArm, error)
	// FlagExperimentUnitValues returns each variant's unit values, at most
	// maxPerArm per variant.
	FlagExperimentUnitValues(ctx context.Context, x FlagExperiment, maxPerArm int) (map[string][]float64, error)
	// SetFlagExperimentBandit stores a bandit pass's posterior and, when
	// weightedAt is set, the weights it published.
	SetFlagExperimentBandit(ctx context.Context, experimentID string, posterior []flags.BanditArm, weights map[string]int, weightedAt *time.Time) error
	// FlagExperimentSegmentArms returns the counted binary arms of the
	// cohort's current members and of everyone else.
	FlagExperimentSegmentArms(ctx context.Context, x FlagExperiment, cohortID string) (members, others []flags.Arm, err error)
}

// FeatureFlagRolloutMetricStore reads a metric-driven rollout stage's
// groups.
type FeatureFlagRolloutMetricStore interface {
	FeatureFlagRolloutMetricArms(ctx context.Context, q RolloutMetricQuery) (comparison, treatment flags.Arm, err error)
}

func orDefault(v, d string) string {
	if v == "" {
		return d
	}
	return v
}

// noDeadline stands for a conversion metric's unbounded horizon.
const noDeadline = 100 * 365 * 24 * time.Hour

// conversionHorizon is how long a unit stays a conversion candidate: a
// funnel's or retention's horizon, or no deadline for a subject
// experiment's conversion metric.
func conversionHorizon(x FlagExperiment) time.Duration {
	if flags.AnalyticsMetric(x.MetricKind) {
		return x.MetricDefinition.Horizon(x.MetricKind)
	}
	return noDeadline
}

func unitRef(id, customer pgtype.UUID, hash []byte, first time.Time) FlagExperimentUnitRef {
	ref := FlagExperimentUnitRef{CustomerID: pgUUIDString(id), FirstExposedAt: first, Customer: pgUUIDString(id)}
	if customer.Valid {
		ref.Customer, ref.SubjectHash = pgUUIDString(customer), hash
	}
	return ref
}

// RecordFlagExperimentExposures implements FlagExperimentExtrasStore.
func (s *PgStore) RecordFlagExperimentExposures(ctx context.Context, accountID, environmentID string, exposures []FlagExperimentExposure) (int, error) {
	if len(exposures) == 0 {
		return 0, nil
	}
	q := sqlc.New()
	running, err := q.RunningSubjectExperiments(ctx, s.pool, uuidToPgtype(environmentID))
	if err != nil || len(running) == 0 {
		return 0, flagExperimentErrOrNil("exposures", err)
	}
	if exposures, err = activeTenantExposures(ctx, q, s.pool, accountID, exposures); err != nil {
		return 0, err
	}
	byRule := map[[2]string]pgtype.UUID{}
	for _, r := range running {
		byRule[[2]string{r.FlagKey, r.RuleID}] = r.ID
	}
	// One row per unit: the variant of its earliest exposure, crossed when
	// the batch saw it with another variant.
	type unit struct {
		e       FlagExperimentExposure
		crossed bool
	}
	units := map[pgtype.UUID]map[string]*unit{}
	accepted := 0
	for _, e := range exposures {
		id, ok := byRule[[2]string{e.Flag, e.RuleID}]
		if !ok {
			continue
		}
		accepted++
		if units[id] == nil {
			units[id] = map[string]*unit{}
		}
		key := SubjectUnitID(e.CustomerID, e.SubjectHash)
		u := units[id][key]
		if u == nil {
			units[id][key] = &unit{e: e}
			continue
		}
		u.crossed = u.crossed || u.e.Variant != e.Variant
		if e.At.Before(u.e.At) {
			u.e.At = e.At
		}
	}
	type batch struct {
		ids, customers []pgtype.UUID
		hashes         [][]byte
		variants       []string
		ats            []pgtype.Timestamptz
		crossed        []bool
	}
	batches := map[pgtype.UUID]*batch{}
	for id, byUnit := range units {
		b := &batch{}
		for key, u := range byUnit {
			b.ids = append(b.ids, uuidToPgtype(key))
			b.customers = append(b.customers, uuidToPgtype(u.e.CustomerID))
			b.hashes = append(b.hashes, u.e.SubjectHash)
			b.variants = append(b.variants, u.e.Variant)
			b.ats = append(b.ats, pgTimestamptzUTC(u.e.At))
			b.crossed = append(b.crossed, u.crossed)
		}
		batches[id] = b
	}
	err = pgx.BeginFunc(ctx, s.pool, func(tx pgx.Tx) error {
		for id, b := range batches {
			if err := q.AddFlagExperimentSubjectUnits(ctx, tx, sqlc.AddFlagExperimentSubjectUnitsParams{ExperimentID: id,
				UnitIds: b.ids, CustomerIds: b.customers, SubjectHashes: b.hashes, Variants: b.variants, FirstAts: b.ats, Crossed: b.crossed}); err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		return 0, fmt.Errorf("flag experiment exposures: %w", err)
	}
	return accepted, nil
}

func flagExperimentErrOrNil(op string, err error) error {
	if err == nil {
		return nil
	}
	return flagExperimentErr(op, err)
}

// FlagExperimentRatioArms implements FlagExperimentExtrasStore.
func (s *PgStore) FlagExperimentRatioArms(ctx context.Context, x FlagExperiment) ([]flags.RatioArm, error) {
	rows, err := sqlc.New().FlagExperimentRatioArms(ctx, s.pool, sqlc.FlagExperimentRatioArmsParams{
		EnvironmentID: uuidToPgtype(x.EnvironmentID), EventName: x.MetricEvent, DenominatorEvent: x.DenominatorEvent,
		OpenFromDay: pgtype.Date{Time: flagExperimentOpenFromDay(x), Valid: true}, ExperimentID: uuidToPgtype(x.ID)})
	if err != nil {
		return nil, flagExperimentErr("ratio arms", err)
	}
	out := make([]flags.RatioArm, 0, len(rows))
	for _, r := range rows {
		out = append(out, flags.RatioArm{Variant: r.Variant, Units: r.Units, Num: r.Num, Den: r.Den, NumSq: r.NumSq, DenSq: r.DenSq, NumDen: r.NumDen})
	}
	return out, nil
}

// FlagExperimentUnitValues implements FlagExperimentExtrasStore.
func (s *PgStore) FlagExperimentUnitValues(ctx context.Context, x FlagExperiment, maxPerArm int) (map[string][]float64, error) {
	rows, err := sqlc.New().FlagExperimentUnitValues(ctx, s.pool, sqlc.FlagExperimentUnitValuesParams{
		EnvironmentID: uuidToPgtype(x.EnvironmentID), EventName: x.MetricEvent,
		OpenFromDay: pgtype.Date{Time: flagExperimentOpenFromDay(x), Valid: true}, ExperimentID: uuidToPgtype(x.ID), MaxRows: int64(maxPerArm)})
	if err != nil {
		return nil, flagExperimentErr("unit values", err)
	}
	out := map[string][]float64{}
	for _, r := range rows {
		out[r.Variant] = append(out[r.Variant], r.Val)
	}
	return out, nil
}

// SetFlagExperimentBandit implements FlagExperimentExtrasStore.
func (s *PgStore) SetFlagExperimentBandit(ctx context.Context, experimentID string, posterior []flags.BanditArm, weights map[string]int, weightedAt *time.Time) error {
	if posterior == nil {
		posterior = []flags.BanditArm{}
	}
	rawPosterior, err := json.Marshal(posterior)
	if err != nil {
		return fmt.Errorf("flag experiment bandit: %w", err)
	}
	rawWeights, err := jsonObject(weights)
	if err != nil {
		return fmt.Errorf("flag experiment bandit: %w", err)
	}
	var at pgtype.Timestamptz
	if weightedAt != nil {
		at = pgTimestamptzUTC(*weightedAt)
	}
	if err := sqlc.New().SetFlagExperimentBandit(ctx, s.pool, sqlc.SetFlagExperimentBanditParams{ID: uuidToPgtype(experimentID),
		Bandit: rawPosterior, Weights: rawWeights, WeightedAt: at}); err != nil {
		return fmt.Errorf("flag experiment bandit: %w", err)
	}
	return nil
}

// FlagExperimentSegmentArms implements FlagExperimentExtrasStore.
func (s *PgStore) FlagExperimentSegmentArms(ctx context.Context, x FlagExperiment, cohortID string) ([]flags.Arm, []flags.Arm, error) {
	horizon := 0.0
	if flags.AnalyticsMetric(x.MetricKind) {
		horizon = x.MetricDefinition.Horizon(x.MetricKind).Seconds()
	}
	rows, err := sqlc.New().FlagExperimentSegmentArms(ctx, s.pool, sqlc.FlagExperimentSegmentArmsParams{CohortID: uuidToPgtype(cohortID),
		ExperimentID: uuidToPgtype(x.ID), HorizonSeconds: horizon, Until: pgTimestamptzUTC(x.TelemetryCursor)})
	if err != nil {
		return nil, nil, flagExperimentErr("segment arms", err)
	}
	var members, others []flags.Arm
	for _, r := range rows {
		a := flags.Arm{Variant: r.Variant, Exposed: r.Exposed, Converted: r.Converted}
		if r.Member {
			members = append(members, a)
		} else {
			others = append(others, a)
		}
	}
	return members, others, nil
}

// FeatureFlagRolloutMetricArms implements FeatureFlagRolloutMetricStore.
func (s *PgStore) FeatureFlagRolloutMetricArms(ctx context.Context, in RolloutMetricQuery) (flags.Arm, flags.Arm, error) {
	rows, err := sqlc.New().FeatureFlagRolloutMetricArms(ctx, s.pool, sqlc.FeatureFlagRolloutMetricArmsParams{
		RuleID: in.RuleID, AccountID: uuidToPgtype(in.AccountID), ProjectID: uuidToPgtype(in.ProjectID), EnvironmentSlug: in.EnvironmentSlug,
		Since: pgTimestamptzUTC(in.Since), FlagKey: in.FlagKey, MaxUnits: int32(in.MaxUnits), EnvironmentID: uuidToPgtype(in.EnvironmentID), EventName: in.Event})
	if err != nil {
		return flags.Arm{}, flags.Arm{}, fmt.Errorf("rollout metric arms: %w", err)
	}
	comparison, treatment := flags.Arm{Variant: "comparison"}, flags.Arm{Variant: "treatment"}
	for _, r := range rows {
		if r.Treated {
			treatment.Exposed, treatment.Converted = r.Exposed, r.Converted
		} else {
			comparison.Exposed, comparison.Converted = r.Exposed, r.Converted
		}
	}
	return comparison, treatment, nil
}

// activeTenantExposures keeps exposures of the account's active customers,
// with their customer ids in canonical form.
func activeTenantExposures(ctx context.Context, q *sqlc.Queries, db sqlc.DBTX, accountID string, exposures []FlagExperimentExposure) ([]FlagExperimentExposure, error) {
	seen := map[string]bool{}
	ids := make([]pgtype.UUID, 0, len(exposures))
	for _, e := range exposures {
		if id := uuidToPgtype(e.CustomerID); id.Valid && !seen[pgUUIDString(id)] {
			seen[pgUUIDString(id)] = true
			ids = append(ids, id)
		}
	}
	if len(ids) == 0 {
		return nil, nil
	}
	rows, err := q.ActivePlatformTenantIDs(ctx, db, sqlc.ActivePlatformTenantIDsParams{AccountID: uuidToPgtype(accountID), Ids: ids})
	if err != nil {
		return nil, fmt.Errorf("flag experiment exposures: customers: %w", err)
	}
	active := map[string]bool{}
	for _, id := range rows {
		active[pgUUIDString(id)] = true
	}
	out := exposures[:0:0]
	for _, e := range exposures {
		if id := uuidToPgtype(e.CustomerID); id.Valid && active[pgUUIDString(id)] {
			e.CustomerID = pgUUIDString(id)
			out = append(out, e)
		}
	}
	return out, nil
}
