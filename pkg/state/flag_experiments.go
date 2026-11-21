package state

import (
	"context"
	"fmt"
	"sort"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/onebox-faas/faas/pkg/state/sqlc"
)

// FlagConversionEvent is one conversion event a runtime SDK reported for a
// verified customer (ADR 20261115163113 §2).
type FlagConversionEvent struct {
	Name       string
	CustomerID string
	Value      int64
	At         time.Time
	// Subject, Properties and Flags are kept only as product analytics
	// (ADR 20261117084505 §1): the subject as a hash, properties bounded per
	// environment, flags as the request's decisions.
	Subject    string
	Properties map[string]string
	Flags      map[string]string
}

// FlagEventIngest is one runtime batch for an environment. Quota is the
// account's monthly event quota; MaxNames bounds distinct event names.
// Analytics stores accepted events as raw product analytics events too, with
// at most MaxPropertyKeys distinct property keys per environment.
type FlagEventIngest struct {
	Scope           FeatureFlagScope
	Quota           int64
	MaxNames        int
	Events          []FlagConversionEvent
	Now             time.Time
	Analytics       bool
	MaxPropertyKeys int
}

// FlagEventIngestResult says what happened to each event of a batch.
type FlagEventIngestResult struct {
	Accepted               int
	DroppedQuota           int
	DroppedUnknownCustomer int
	DroppedNameLimit       int
	MonthAccepted          int64
	MonthDropped           int64
}

// FlagEventStore stores conversion events and sweeps them with the plan's
// request telemetry retention.
type FlagEventStore interface {
	IngestFlagEvents(ctx context.Context, in FlagEventIngest) (FlagEventIngestResult, error)
	// SweepFlagConversions deletes daily rows older than each account's
	// retention; retentionDays maps a plan to its request telemetry
	// retention (0 keeps nothing).
	SweepFlagConversions(ctx context.Context, now time.Time, retentionDays func(plan string) int, maxAccounts int) (int64, error)
}

type flagConversionKey struct {
	name, customer string
	day            time.Time
}

type flagConversionFold struct {
	count, value int64
	first, last  time.Time
	customerPgID pgtype.UUID
}

// IngestFlagEvents drops events for customers that are not active platform
// tenants of the account and events whose name would exceed the
// environment's name bound, then takes the remaining events from the
// month's quota and folds them into daily rows, all in one transaction.
func (s *PgStore) IngestFlagEvents(ctx context.Context, in FlagEventIngest) (FlagEventIngestResult, error) {
	var res FlagEventIngestResult
	acct, env := uuidToPgtype(in.Scope.AccountID), uuidToPgtype(in.Scope.EnvironmentID)
	if !acct.Valid || !env.Valid {
		return res, fmt.Errorf("flag events: invalid scope")
	}
	month := time.Date(in.Now.UTC().Year(), in.Now.UTC().Month(), 1, 0, 0, 0, 0, time.UTC)
	err := pgx.BeginFunc(ctx, s.pool, func(tx pgx.Tx) error {
		q := sqlc.New()
		events, err := activeTenantEvents(ctx, q, tx, acct, in.Events, &res)
		if err != nil {
			return err
		}
		if events, err = knownNameEvents(ctx, q, tx, in, acct, env, events, &res); err != nil {
			return err
		}
		pgMonth := pgtype.Date{Time: month, Valid: true}
		if err := q.EnsureFlagEventUsage(ctx, tx, sqlc.EnsureFlagEventUsageParams{AccountID: acct, Month: pgMonth}); err != nil {
			return fmt.Errorf("flag events: usage: %w", err)
		}
		usage, err := q.LockFlagEventUsage(ctx, tx, sqlc.LockFlagEventUsageParams{AccountID: acct, Month: pgMonth})
		if err != nil {
			return fmt.Errorf("flag events: lock usage: %w", err)
		}
		granted := int64(len(events))
		if left := in.Quota - usage.Accepted; granted > left {
			granted = max(left, 0)
		}
		res.DroppedQuota = len(events) - int(granted)
		events = events[:granted]
		totals, err := q.AddFlagEventUsage(ctx, tx, sqlc.AddFlagEventUsageParams{
			AccountID: acct, Month: pgMonth, Accepted: granted, Dropped: int64(res.DroppedQuota)})
		if err != nil {
			return fmt.Errorf("flag events: add usage: %w", err)
		}
		res.MonthAccepted, res.MonthDropped = totals.Accepted, totals.Dropped
		res.Accepted = len(events)
		if err := foldFlagConversions(ctx, q, tx, acct, env, events); err != nil {
			return err
		}
		if !in.Analytics || len(events) == 0 {
			return nil
		}
		return insertAnalyticsEvents(ctx, q, tx, in, acct, env, events)
	})
	if err != nil {
		return FlagEventIngestResult{}, err
	}
	return res, nil
}

func activeTenantEvents(ctx context.Context, q *sqlc.Queries, tx pgx.Tx, acct pgtype.UUID, events []FlagConversionEvent, res *FlagEventIngestResult) ([]FlagConversionEvent, error) {
	seen := map[string]bool{}
	ids := make([]pgtype.UUID, 0, len(events))
	for _, e := range events {
		if id := uuidToPgtype(e.CustomerID); id.Valid && !seen[pgUUIDString(id)] {
			seen[pgUUIDString(id)] = true
			ids = append(ids, id)
		}
	}
	active := map[string]bool{}
	if len(ids) > 0 {
		rows, err := q.ActivePlatformTenantIDs(ctx, tx, sqlc.ActivePlatformTenantIDsParams{AccountID: acct, Ids: ids})
		if err != nil {
			return nil, fmt.Errorf("flag events: customers: %w", err)
		}
		for _, id := range rows {
			active[pgUUIDString(id)] = true
		}
	}
	out := events[:0:0]
	for _, e := range events {
		if id := uuidToPgtype(e.CustomerID); id.Valid && active[pgUUIDString(id)] {
			e.CustomerID = pgUUIDString(id)
			out = append(out, e)
			continue
		}
		res.DroppedUnknownCustomer++
	}
	return out, nil
}

func knownNameEvents(ctx context.Context, q *sqlc.Queries, tx pgx.Tx, in FlagEventIngest, acct, env pgtype.UUID, events []FlagConversionEvent, res *FlagEventIngestResult) ([]FlagConversionEvent, error) {
	names := map[string]bool{}
	for _, e := range events {
		names[e.Name] = false
	}
	list := make([]string, 0, len(names))
	for n := range names {
		list = append(list, n)
	}
	sort.Strings(list)
	for _, n := range list {
		if _, err := q.AddFlagEventName(ctx, tx, sqlc.AddFlagEventNameParams{EnvironmentID: env, Name: n, AccountID: acct, MaxNames: int64(in.MaxNames)}); err != nil {
			return nil, fmt.Errorf("flag events: name: %w", err)
		}
	}
	known, err := q.FlagEventNamesKnown(ctx, tx, sqlc.FlagEventNamesKnownParams{EnvironmentID: env, Names: list})
	if err != nil {
		return nil, fmt.Errorf("flag events: names: %w", err)
	}
	for _, n := range known {
		names[n] = true
	}
	out := events[:0:0]
	for _, e := range events {
		if names[e.Name] {
			out = append(out, e)
			continue
		}
		res.DroppedNameLimit++
	}
	return out, nil
}

func foldFlagConversions(ctx context.Context, q *sqlc.Queries, tx pgx.Tx, acct, env pgtype.UUID, events []FlagConversionEvent) error {
	folds := map[flagConversionKey]*flagConversionFold{}
	keys := []flagConversionKey{}
	for _, e := range events {
		at := e.At.UTC()
		k := flagConversionKey{name: e.Name, customer: e.CustomerID, day: time.Date(at.Year(), at.Month(), at.Day(), 0, 0, 0, 0, time.UTC)}
		f, ok := folds[k]
		if !ok {
			f = &flagConversionFold{first: at, last: at, customerPgID: uuidToPgtype(e.CustomerID)}
			folds[k] = f
			keys = append(keys, k)
		}
		f.count++
		f.value += e.Value
		if at.Before(f.first) {
			f.first = at
		}
		if at.After(f.last) {
			f.last = at
		}
	}
	// A stable order keeps concurrent batches from deadlocking on the rows.
	sort.Slice(keys, func(i, j int) bool {
		a, b := keys[i], keys[j]
		if a.name != b.name {
			return a.name < b.name
		}
		if a.customer != b.customer {
			return a.customer < b.customer
		}
		return a.day.Before(b.day)
	})
	for _, k := range keys {
		f := folds[k]
		if err := q.AddFlagConversionDaily(ctx, tx, sqlc.AddFlagConversionDailyParams{
			EnvironmentID: env, EventName: k.name, CustomerID: f.customerPgID, Day: pgtype.Date{Time: k.day, Valid: true},
			AccountID: acct, Count: f.count, ValueSum: f.value, FirstAt: pgTimestamptzUTC(f.first), LastAt: pgTimestamptzUTC(f.last),
		}); err != nil {
			return fmt.Errorf("flag events: fold: %w", err)
		}
	}
	return nil
}

// SweepFlagConversions implements FlagEventStore.
func (s *PgStore) SweepFlagConversions(ctx context.Context, now time.Time, retentionDays func(plan string) int, maxAccounts int) (int64, error) {
	q := sqlc.New()
	today := time.Date(now.UTC().Year(), now.UTC().Month(), now.UTC().Day(), 0, 0, 0, 0, time.UTC)
	// Every plan keeps at most its telemetry retention, so rows older than
	// yesterday are candidates; each account then uses its own bound.
	accounts, err := q.FlagConversionAccounts(ctx, s.pool, sqlc.FlagConversionAccountsParams{
		Cutoff: pgtype.Date{Time: today, Valid: true}, MaxAccounts: int32(maxAccounts)})
	if err != nil {
		return 0, fmt.Errorf("flag conversions sweep: %w", err)
	}
	var deleted int64
	for _, a := range accounts {
		before := today.AddDate(0, 0, -retentionDays(a.Plan))
		n, err := q.DeleteFlagConversionsBefore(ctx, s.pool, sqlc.DeleteFlagConversionsBeforeParams{
			AccountID: a.AccountID, Before: pgtype.Date{Time: before, Valid: true}})
		if err != nil {
			return deleted, fmt.Errorf("flag conversions sweep: %w", err)
		}
		deleted += n
	}
	return deleted, nil
}
