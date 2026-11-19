package state

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"sort"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/onebox-faas/faas/pkg/state/sqlc"
)

// Product analytics events (ADR 20261117084505).

// Analytics units: a query counts customers or end-user subjects.
const (
	AnalyticsUnitCustomer = "customer"
	AnalyticsUnitSubject  = "subject"
)

// AnalyticsSubjectHash is the stored form of an opaque end-user subject:
// sha256 over the environment and the subject, so one app identifier never
// links across environments and is never stored.
func AnalyticsSubjectHash(environmentID, subject string) []byte {
	h := sha256.New()
	h.Write([]byte(environmentID))
	h.Write([]byte{0})
	h.Write([]byte(subject))
	return h.Sum(nil)
}

// AnalyticsUnitRef is one unit: a customer ID, or a subject hash.
type AnalyticsUnitRef struct {
	CustomerID  string
	SubjectHash []byte
}

// Key identifies the unit within one query's unit kind.
func (u AnalyticsUnitRef) Key() string {
	if u.SubjectHash != nil {
		return hex.EncodeToString(u.SubjectHash)
	}
	return u.CustomerID
}

// AnalyticsMatch selects events: an environment, a name, a time range
// [From, Until) and property values every event must carry.
type AnalyticsMatch struct {
	EnvironmentID string
	Name          string
	From, Until   time.Time
	Properties    map[string]string
}

// AnalyticsSeriesQuery is an event series: Bucket is the bucket width, ByKind
// is "property", "flag" or "", and Unit is a unit kind.
type AnalyticsSeriesQuery struct {
	AnalyticsMatch
	Bucket        time.Duration
	ByKind, ByKey string
	Unit          string
}

// AnalyticsSeriesRow is one bucket and breakdown value.
type AnalyticsSeriesRow struct {
	BucketStart time.Time
	Breakdown   string
	Events      int64
	Units       int64
	ValueSum    int64
}

// AnalyticsEntry is a unit's first matching event in a range.
type AnalyticsEntry struct {
	Unit       AnalyticsUnitRef
	At         time.Time
	Properties map[string]string
	Flags      map[string]string
}

// AnalyticsStep asks for each unit's first matching event after After and by
// Deadline.
type AnalyticsStep struct {
	Unit     AnalyticsUnitRef
	After    time.Time
	Deadline time.Time
}

// AnalyticsCohortUnit is a retention unit with its first start event, cohort
// start and breakdown value.
type AnalyticsCohortUnit struct {
	Unit      AnalyticsUnitRef
	FirstAt   time.Time
	Cohort    time.Time
	Breakdown string
}

// AnalyticsReturnRow counts a cohort's units that came back in a period.
type AnalyticsReturnRow struct {
	Cohort    time.Time
	Breakdown string
	Period    int
	Units     int64
}

// AnalyticsRequestRow is request telemetry for one flag value and latency
// bucket.
type AnalyticsRequestRow struct {
	Value    string
	Bucket   int
	Requests int64
	Errors   int64
}

// AnalyticsRequestsQuery reads request telemetry by flag value for a project
// environment.
type AnalyticsRequestsQuery struct {
	AccountID, ProjectID, EnvironmentSlug string
	Flag                                  string
	From, Until                           time.Time
}

// AnalyticsStore stores raw events and answers the bounded primitives that
// series, funnels and retention are built from.
type AnalyticsStore interface {
	AnalyticsEventSeries(ctx context.Context, q AnalyticsSeriesQuery) ([]AnalyticsSeriesRow, error)
	// AnalyticsEntries returns each unit's first matching event, at most
	// maxUnits units.
	AnalyticsEntries(ctx context.Context, m AnalyticsMatch, unit string, maxUnits int) ([]AnalyticsEntry, error)
	// AnalyticsNextSteps returns, by unit key, each unit's first event
	// matching m.Name and m.Properties after its step's After and by its
	// Deadline; m's range is ignored.
	AnalyticsNextSteps(ctx context.Context, m AnalyticsMatch, unit string, steps []AnalyticsStep) (map[string]time.Time, error)
	// AnalyticsUnitFlags returns, by unit key, each unit's value for flag on
	// its earliest event in [from, until) that carries the flag.
	AnalyticsUnitFlags(ctx context.Context, environmentID, unit, flag string, units []AnalyticsUnitRef, from, until time.Time) (map[string]string, error)
	// AnalyticsReturns counts, per cohort, breakdown and period 1..periods,
	// the units that sent an event matching m after their first start event.
	AnalyticsReturns(ctx context.Context, m AnalyticsMatch, unit string, units []AnalyticsCohortUnit, period time.Duration, periods int) ([]AnalyticsReturnRow, error)
	AnalyticsRequestsByFlag(ctx context.Context, q AnalyticsRequestsQuery) ([]AnalyticsRequestRow, error)
	// SweepAnalyticsEvents deletes events past each plan's retention, at
	// most batch rows per statement and batches per call.
	SweepAnalyticsEvents(ctx context.Context, now time.Time, retentionDays map[string]int, batch, batches int) (int64, error)
}

func analyticsFilter(props map[string]string) ([]byte, error) {
	if len(props) == 0 {
		return []byte("{}"), nil
	}
	return json.Marshal(props)
}

func decodeStringMap(raw []byte) map[string]string {
	out := map[string]string{}
	if len(raw) > 0 {
		_ = json.Unmarshal(raw, &out)
	}
	return out
}

// insertAnalyticsEvents keeps the properties whose keys the environment
// already holds or can still register, and inserts the batch's raw events.
func insertAnalyticsEvents(ctx context.Context, q *sqlc.Queries, tx pgx.Tx, in FlagEventIngest, acct, env pgtype.UUID, events []FlagConversionEvent) error {
	known, err := analyticsPropertyKeys(ctx, q, tx, env, events, in.MaxPropertyKeys)
	if err != nil {
		return err
	}
	p := sqlc.InsertAnalyticsEventsParams{AccountID: acct, EnvironmentID: env}
	for _, e := range events {
		props := map[string]string{}
		for k, v := range e.Properties {
			if known[k] {
				props[k] = v
			}
		}
		propsJSON, err := json.Marshal(props)
		if err != nil {
			return fmt.Errorf("analytics events: properties: %w", err)
		}
		flagsMap := e.Flags
		if flagsMap == nil {
			flagsMap = map[string]string{}
		}
		flagsJSON, err := json.Marshal(flagsMap)
		if err != nil {
			return fmt.Errorf("analytics events: flags: %w", err)
		}
		var subject []byte
		if e.Subject != "" {
			subject = AnalyticsSubjectHash(in.Scope.EnvironmentID, e.Subject)
		}
		p.CustomerIds = append(p.CustomerIds, uuidToPgtype(e.CustomerID))
		p.SubjectHashes = append(p.SubjectHashes, subject)
		p.Names = append(p.Names, e.Name)
		p.Ats = append(p.Ats, pgTimestamptzUTC(e.At))
		p.EventValues = append(p.EventValues, e.Value)
		p.Properties = append(p.Properties, string(propsJSON))
		p.Flags = append(p.Flags, string(flagsJSON))
	}
	if err := q.InsertAnalyticsEvents(ctx, tx, p); err != nil {
		return fmt.Errorf("analytics events: insert: %w", err)
	}
	return nil
}

func analyticsPropertyKeys(ctx context.Context, q *sqlc.Queries, tx pgx.Tx, env pgtype.UUID, events []FlagConversionEvent, maxKeys int) (map[string]bool, error) {
	set := map[string]bool{}
	for _, e := range events {
		for k := range e.Properties {
			set[k] = false
		}
	}
	if len(set) == 0 {
		return set, nil
	}
	keys := make([]string, 0, len(set))
	for k := range set {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		if _, err := q.AddAnalyticsPropertyKey(ctx, tx, sqlc.AddAnalyticsPropertyKeyParams{EnvironmentID: env, Key: k, MaxKeys: int64(maxKeys)}); err != nil {
			return nil, fmt.Errorf("analytics events: property key: %w", err)
		}
	}
	known, err := q.AnalyticsPropertyKeysKnown(ctx, tx, sqlc.AnalyticsPropertyKeysKnownParams{EnvironmentID: env, Keys: keys})
	if err != nil {
		return nil, fmt.Errorf("analytics events: property keys: %w", err)
	}
	for _, k := range known {
		set[k] = true
	}
	return set, nil
}

// AnalyticsEventSeries implements AnalyticsStore.
func (s *PgStore) AnalyticsEventSeries(ctx context.Context, in AnalyticsSeriesQuery) ([]AnalyticsSeriesRow, error) {
	filter, err := analyticsFilter(in.Properties)
	if err != nil {
		return nil, err
	}
	rows, err := sqlc.New().AnalyticsEventSeries(ctx, s.pool, sqlc.AnalyticsEventSeriesParams{
		BucketSeconds: in.Bucket.Seconds(), Origin: pgTimestamptzUTC(in.From), ByKind: in.ByKind, ByKey: in.ByKey, Unit: in.Unit,
		EnvironmentID: uuidToPgtype(in.EnvironmentID), Name: in.Name, RangeFrom: pgTimestamptzUTC(in.From),
		RangeUntil: pgTimestamptzUTC(in.Until), Filter: filter})
	if err != nil {
		return nil, fmt.Errorf("analytics series: %w", err)
	}
	out := make([]AnalyticsSeriesRow, 0, len(rows))
	for _, r := range rows {
		out = append(out, AnalyticsSeriesRow{BucketStart: r.BucketStart.Time.UTC(), Breakdown: r.Breakdown, Events: r.Events, Units: r.Units, ValueSum: r.ValueSum})
	}
	return out, nil
}

// AnalyticsEntries implements AnalyticsStore.
func (s *PgStore) AnalyticsEntries(ctx context.Context, m AnalyticsMatch, unit string, maxUnits int) ([]AnalyticsEntry, error) {
	filter, err := analyticsFilter(m.Properties)
	if err != nil {
		return nil, err
	}
	q := sqlc.New()
	if unit == AnalyticsUnitSubject {
		rows, err := q.AnalyticsEntriesBySubject(ctx, s.pool, sqlc.AnalyticsEntriesBySubjectParams{
			EnvironmentID: uuidToPgtype(m.EnvironmentID), Name: m.Name, RangeFrom: pgTimestamptzUTC(m.From),
			RangeUntil: pgTimestamptzUTC(m.Until), Filter: filter, MaxRows: int32(maxUnits)})
		if err != nil {
			return nil, fmt.Errorf("analytics entries: %w", err)
		}
		out := make([]AnalyticsEntry, 0, len(rows))
		for _, r := range rows {
			out = append(out, AnalyticsEntry{Unit: AnalyticsUnitRef{SubjectHash: r.SubjectHash}, At: r.At.Time.UTC(),
				Properties: decodeStringMap(r.Properties), Flags: decodeStringMap(r.Flags)})
		}
		return out, nil
	}
	rows, err := q.AnalyticsEntriesByCustomer(ctx, s.pool, sqlc.AnalyticsEntriesByCustomerParams{
		EnvironmentID: uuidToPgtype(m.EnvironmentID), Name: m.Name, RangeFrom: pgTimestamptzUTC(m.From),
		RangeUntil: pgTimestamptzUTC(m.Until), Filter: filter, MaxRows: int32(maxUnits)})
	if err != nil {
		return nil, fmt.Errorf("analytics entries: %w", err)
	}
	out := make([]AnalyticsEntry, 0, len(rows))
	for _, r := range rows {
		out = append(out, AnalyticsEntry{Unit: AnalyticsUnitRef{CustomerID: pgUUIDString(r.CustomerID)}, At: r.At.Time.UTC(),
			Properties: decodeStringMap(r.Properties), Flags: decodeStringMap(r.Flags)})
	}
	return out, nil
}

// AnalyticsNextSteps implements AnalyticsStore.
func (s *PgStore) AnalyticsNextSteps(ctx context.Context, m AnalyticsMatch, unit string, steps []AnalyticsStep) (map[string]time.Time, error) {
	out := map[string]time.Time{}
	if len(steps) == 0 {
		return out, nil
	}
	filter, err := analyticsFilter(m.Properties)
	if err != nil {
		return nil, err
	}
	afters := make([]pgtype.Timestamptz, 0, len(steps))
	deadlines := make([]pgtype.Timestamptz, 0, len(steps))
	for _, st := range steps {
		afters = append(afters, pgTimestamptzUTC(st.After))
		deadlines = append(deadlines, pgTimestamptzUTC(st.Deadline))
	}
	q := sqlc.New()
	if unit == AnalyticsUnitSubject {
		hashes := make([][]byte, 0, len(steps))
		for _, st := range steps {
			hashes = append(hashes, st.Unit.SubjectHash)
		}
		rows, err := q.AnalyticsNextStepSubjects(ctx, s.pool, sqlc.AnalyticsNextStepSubjectsParams{
			SubjectHashes: hashes, AfterAts: afters, Deadlines: deadlines, EnvironmentID: uuidToPgtype(m.EnvironmentID), Name: m.Name, Filter: filter})
		if err != nil {
			return nil, fmt.Errorf("analytics next step: %w", err)
		}
		for _, r := range rows {
			out[hex.EncodeToString(r.SubjectHash)] = r.At.Time.UTC()
		}
		return out, nil
	}
	ids := make([]pgtype.UUID, 0, len(steps))
	for _, st := range steps {
		ids = append(ids, uuidToPgtype(st.Unit.CustomerID))
	}
	rows, err := q.AnalyticsNextStepCustomers(ctx, s.pool, sqlc.AnalyticsNextStepCustomersParams{
		CustomerIds: ids, AfterAts: afters, Deadlines: deadlines, EnvironmentID: uuidToPgtype(m.EnvironmentID), Name: m.Name, Filter: filter})
	if err != nil {
		return nil, fmt.Errorf("analytics next step: %w", err)
	}
	for _, r := range rows {
		out[pgUUIDString(r.CustomerID)] = r.At.Time.UTC()
	}
	return out, nil
}

// AnalyticsUnitFlags implements AnalyticsStore.
func (s *PgStore) AnalyticsUnitFlags(ctx context.Context, environmentID, unit, flag string, units []AnalyticsUnitRef, from, until time.Time) (map[string]string, error) {
	out := map[string]string{}
	if len(units) == 0 {
		return out, nil
	}
	q := sqlc.New()
	if unit == AnalyticsUnitSubject {
		hashes := make([][]byte, 0, len(units))
		for _, u := range units {
			hashes = append(hashes, u.SubjectHash)
		}
		rows, err := q.AnalyticsUnitFlagSubjects(ctx, s.pool, sqlc.AnalyticsUnitFlagSubjectsParams{Flag: flag,
			EnvironmentID: uuidToPgtype(environmentID), SubjectHashes: hashes, RangeFrom: pgTimestamptzUTC(from), RangeUntil: pgTimestamptzUTC(until)})
		if err != nil {
			return nil, fmt.Errorf("analytics unit flags: %w", err)
		}
		for _, r := range rows {
			out[hex.EncodeToString(r.SubjectHash)] = r.Value
		}
		return out, nil
	}
	ids := make([]pgtype.UUID, 0, len(units))
	for _, u := range units {
		ids = append(ids, uuidToPgtype(u.CustomerID))
	}
	rows, err := q.AnalyticsUnitFlagCustomers(ctx, s.pool, sqlc.AnalyticsUnitFlagCustomersParams{Flag: flag,
		CustomerIds: ids, EnvironmentID: uuidToPgtype(environmentID), RangeFrom: pgTimestamptzUTC(from), RangeUntil: pgTimestamptzUTC(until)})
	if err != nil {
		return nil, fmt.Errorf("analytics unit flags: %w", err)
	}
	for _, r := range rows {
		out[pgUUIDString(r.CustomerID)] = r.Value
	}
	return out, nil
}

// AnalyticsReturns implements AnalyticsStore.
func (s *PgStore) AnalyticsReturns(ctx context.Context, m AnalyticsMatch, unit string, units []AnalyticsCohortUnit, period time.Duration, periods int) ([]AnalyticsReturnRow, error) {
	if len(units) == 0 {
		return nil, nil
	}
	filter, err := analyticsFilter(m.Properties)
	if err != nil {
		return nil, err
	}
	firsts := make([]pgtype.Timestamptz, 0, len(units))
	cohorts := make([]pgtype.Timestamptz, 0, len(units))
	breakdowns := make([]string, 0, len(units))
	for _, u := range units {
		firsts = append(firsts, pgTimestamptzUTC(u.FirstAt))
		cohorts = append(cohorts, pgTimestamptzUTC(u.Cohort))
		breakdowns = append(breakdowns, u.Breakdown)
	}
	q := sqlc.New()
	var out []AnalyticsReturnRow
	if unit == AnalyticsUnitSubject {
		hashes := make([][]byte, 0, len(units))
		for _, u := range units {
			hashes = append(hashes, u.Unit.SubjectHash)
		}
		rows, err := q.AnalyticsReturnsSubjects(ctx, s.pool, sqlc.AnalyticsReturnsSubjectsParams{
			SubjectHashes: hashes, FirstAts: firsts, Cohorts: cohorts, Breakdowns: breakdowns, EnvironmentID: uuidToPgtype(m.EnvironmentID),
			PeriodSeconds: period.Seconds(), Periods: int32(periods), Name: m.Name, Filter: filter})
		if err != nil {
			return nil, fmt.Errorf("analytics returns: %w", err)
		}
		for _, r := range rows {
			out = append(out, AnalyticsReturnRow{Cohort: r.Cohort.Time.UTC(), Breakdown: r.Breakdown, Period: int(r.Period), Units: r.Units})
		}
		return out, nil
	}
	ids := make([]pgtype.UUID, 0, len(units))
	for _, u := range units {
		ids = append(ids, uuidToPgtype(u.Unit.CustomerID))
	}
	rows, err := q.AnalyticsReturnsCustomers(ctx, s.pool, sqlc.AnalyticsReturnsCustomersParams{
		CustomerIds: ids, FirstAts: firsts, Cohorts: cohorts, Breakdowns: breakdowns, PeriodSeconds: period.Seconds(),
		Periods: int32(periods), EnvironmentID: uuidToPgtype(m.EnvironmentID), Name: m.Name, Filter: filter})
	if err != nil {
		return nil, fmt.Errorf("analytics returns: %w", err)
	}
	for _, r := range rows {
		out = append(out, AnalyticsReturnRow{Cohort: r.Cohort.Time.UTC(), Breakdown: r.Breakdown, Period: int(r.Period), Units: r.Units})
	}
	return out, nil
}

// AnalyticsRequestsByFlag implements AnalyticsStore.
func (s *PgStore) AnalyticsRequestsByFlag(ctx context.Context, in AnalyticsRequestsQuery) ([]AnalyticsRequestRow, error) {
	rows, err := sqlc.New().AnalyticsRequestsByFlag(ctx, s.pool, sqlc.AnalyticsRequestsByFlagParams{
		AccountID: uuidToPgtype(in.AccountID), ProjectID: uuidToPgtype(in.ProjectID), EnvironmentSlug: in.EnvironmentSlug,
		ReceivedFrom: pgTimestamptzUTC(in.From), ReceivedUntil: pgTimestamptzUTC(in.Until), Flag: in.Flag})
	if err != nil {
		return nil, fmt.Errorf("analytics requests by flag: %w", err)
	}
	out := make([]AnalyticsRequestRow, 0, len(rows))
	for _, r := range rows {
		out = append(out, AnalyticsRequestRow{Value: r.Value, Bucket: int(r.Bucket), Requests: r.Requests, Errors: r.Errors})
	}
	return out, nil
}

// SweepAnalyticsEvents implements AnalyticsStore. Plans with the shortest
// retention go first, so each plan's statement only meets its own expired
// rows; plans missing from retentionDays keep nothing.
func (s *PgStore) SweepAnalyticsEvents(ctx context.Context, now time.Time, retentionDays map[string]int, batch, batches int) (int64, error) {
	plans := make([]string, 0, len(retentionDays))
	for p := range retentionDays {
		plans = append(plans, p)
	}
	sort.Slice(plans, func(i, j int) bool {
		if retentionDays[plans[i]] != retentionDays[plans[j]] {
			return retentionDays[plans[i]] < retentionDays[plans[j]]
		}
		return plans[i] < plans[j]
	})
	q := sqlc.New()
	var deleted int64
	run := func(del func() (int64, error)) error {
		for i := 0; i < batches; i++ {
			n, err := del()
			if err != nil {
				return fmt.Errorf("analytics sweep: %w", err)
			}
			deleted += n
			if n < int64(batch) {
				return nil
			}
		}
		return nil
	}
	if err := run(func() (int64, error) {
		return q.DeleteAnalyticsEventsOutsidePlans(ctx, s.pool, sqlc.DeleteAnalyticsEventsOutsidePlansParams{KnownPlans: plans, MaxRows: int32(batch)})
	}); err != nil {
		return deleted, err
	}
	for _, p := range plans {
		cutoff := now.UTC().AddDate(0, 0, -retentionDays[p])
		if err := run(func() (int64, error) {
			return q.DeleteExpiredAnalyticsEventsForPlan(ctx, s.pool, sqlc.DeleteExpiredAnalyticsEventsForPlanParams{
				Cutoff: pgTimestamptzUTC(cutoff), Plan: p, MaxRows: int32(batch)})
		}); err != nil {
			return deleted, err
		}
	}
	return deleted, nil
}
