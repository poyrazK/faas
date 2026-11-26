package state

import (
	"context"
	"sort"
	"strings"
	"time"
)

// Seller stats (ADR-20261126082046): app-level aggregates over the consumer
// usage, statement and plan tables, so a seller's stats cost a few queries
// rather than several per consumer.

// APIConsumerMonthUsage is one consumer's usage in one UTC calendar month.
type APIConsumerMonthUsage struct {
	ConsumerKey   string
	Month         time.Time
	Requests      int64
	BillableUnits int64
}

// APIConsumerRouteTotal is one route's billable units and distinct
// consumers over a window.
type APIConsumerRouteTotal struct {
	Route         string
	BillableUnits int64
	Consumers     int64
}

// SellerStatsStore reads the aggregates behind GET /v1/apps/{slug}/mcp-stats.
type SellerStatsStore interface {
	// SumAPIConsumerUsageByMonth sums usage minutes in [since, until) by
	// consumer and UTC month.
	SumAPIConsumerUsageByMonth(ctx context.Context, accountID, appID string, since, until time.Time) ([]APIConsumerMonthUsage, error)
	// SumAPIConsumerRouteUsage sums route usage minutes in [since, until)
	// for routes starting with prefix, by route, with distinct consumers.
	SumAPIConsumerRouteUsage(ctx context.Context, accountID, appID, prefix string, since, until time.Time) ([]APIConsumerRouteTotal, error)
	// ListFinalizedAPIConsumerStatementsForApp lists the app's finalized
	// statements whose period starts in [since, until).
	ListFinalizedAPIConsumerStatementsForApp(ctx context.Context, accountID, appID string, since, until time.Time) ([]APIConsumerUsageStatement, error)
	// ListAPIConsumerPlanAssignmentsForApp lists every consumer's plan
	// assignments, oldest first.
	ListAPIConsumerPlanAssignmentsForApp(ctx context.Context, accountID, appID string) ([]APIConsumerPlanAssignment, error)
}

// UTCMonthStart is the start of t's UTC calendar month.
func UTCMonthStart(t time.Time) time.Time {
	t = t.UTC()
	return time.Date(t.Year(), t.Month(), 1, 0, 0, 0, 0, time.UTC)
}

func (m *MemStore) SumAPIConsumerUsageByMonth(_ context.Context, accountID, appID string, since, until time.Time) ([]APIConsumerMonthUsage, error) {
	if accountID == "" || appID == "" || !until.After(since) {
		return nil, ErrNotFound
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	sums := map[[2]string]*APIConsumerMonthUsage{}
	for _, b := range m.apiConsumerUsage {
		if b.AccountID != accountID || b.AppID != appID || b.WindowStart.Before(since) || !b.WindowStart.Before(until) {
			continue
		}
		month := UTCMonthStart(b.WindowStart)
		k := [2]string{b.ConsumerKey, month.Format(time.RFC3339)}
		s := sums[k]
		if s == nil {
			s = &APIConsumerMonthUsage{ConsumerKey: b.ConsumerKey, Month: month}
			sums[k] = s
		}
		s.Requests += b.RequestCount
		s.BillableUnits += b.BillableUnits
	}
	out := make([]APIConsumerMonthUsage, 0, len(sums))
	for _, s := range sums {
		out = append(out, *s)
	}
	sort.Slice(out, func(i, j int) bool {
		if !out[i].Month.Equal(out[j].Month) {
			return out[i].Month.Before(out[j].Month)
		}
		return out[i].ConsumerKey < out[j].ConsumerKey
	})
	return out, nil
}

func (m *MemStore) SumAPIConsumerRouteUsage(_ context.Context, accountID, appID, prefix string, since, until time.Time) ([]APIConsumerRouteTotal, error) {
	if accountID == "" || appID == "" || !until.After(since) {
		return nil, ErrNotFound
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	keyPrefix := accountID + "\x00" + appID + "\x00"
	units := map[string]int64{}
	consumers := map[string]map[string]bool{}
	for key, b := range m.apiConsumerRouteUsage {
		if !strings.HasPrefix(key, keyPrefix) || !strings.HasPrefix(b.Route, prefix) || b.WindowStart.Before(since) || !b.WindowStart.Before(until) {
			continue
		}
		consumer, _, _ := strings.Cut(strings.TrimPrefix(key, keyPrefix), "\x00")
		units[b.Route] += b.BillableUnits
		if consumers[b.Route] == nil {
			consumers[b.Route] = map[string]bool{}
		}
		consumers[b.Route][consumer] = true
	}
	out := make([]APIConsumerRouteTotal, 0, len(units))
	for route, u := range units {
		out = append(out, APIConsumerRouteTotal{Route: route, BillableUnits: u, Consumers: int64(len(consumers[route]))})
	}
	sortRouteTotals(out)
	return out, nil
}

func sortRouteTotals(out []APIConsumerRouteTotal) {
	sort.Slice(out, func(i, j int) bool {
		if out[i].BillableUnits != out[j].BillableUnits {
			return out[i].BillableUnits > out[j].BillableUnits
		}
		return out[i].Route < out[j].Route
	})
}

func (m *MemStore) ListFinalizedAPIConsumerStatementsForApp(_ context.Context, accountID, appID string, since, until time.Time) ([]APIConsumerUsageStatement, error) {
	if accountID == "" || appID == "" || !until.After(since) {
		return nil, ErrNotFound
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	out := []APIConsumerUsageStatement{}
	for _, s := range m.apiConsumerUsageStatements {
		if s.AccountID == accountID && s.AppID == appID && s.Status == APIConsumerUsageStatementFinalized &&
			!s.PeriodStart.Before(since) && s.PeriodStart.Before(until) {
			out = append(out, s)
		}
	}
	sort.Slice(out, func(i, j int) bool {
		if !out[i].PeriodStart.Equal(out[j].PeriodStart) {
			return out[i].PeriodStart.Before(out[j].PeriodStart)
		}
		return out[i].ID < out[j].ID
	})
	return out, nil
}

func (m *MemStore) ListAPIConsumerPlanAssignmentsForApp(_ context.Context, accountID, appID string) ([]APIConsumerPlanAssignment, error) {
	if accountID == "" || appID == "" {
		return nil, ErrNotFound
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	out := []APIConsumerPlanAssignment{}
	for _, a := range m.apiConsumerPlanAssignments {
		if a.AccountID == accountID && a.AppID == appID {
			out = append(out, a)
		}
	}
	sort.Slice(out, func(i, j int) bool {
		if !out[i].EffectiveFrom.Equal(out[j].EffectiveFrom) {
			return out[i].EffectiveFrom.Before(out[j].EffectiveFrom)
		}
		return out[i].ID < out[j].ID
	})
	return out, nil
}

func (s *PgStore) SumAPIConsumerUsageByMonth(ctx context.Context, accountID, appID string, since, until time.Time) ([]APIConsumerMonthUsage, error) {
	if accountID == "" || appID == "" || !until.After(since) {
		return nil, ErrNotFound
	}
	rows, err := s.pool.Query(ctx, `
		select consumer_key, date_trunc('month', window_start, 'UTC') as month,
		       coalesce(sum(request_count), 0)::bigint, coalesce(sum(billable_units), 0)::bigint
		  from api_consumer_usage_minutes
		 where account_id = $1::uuid and app_id = $2::uuid and window_start >= $3 and window_start < $4
		 group by consumer_key, month
		 order by month, consumer_key`, accountID, appID, since.UTC(), until.UTC())
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []APIConsumerMonthUsage{}
	for rows.Next() {
		var u APIConsumerMonthUsage
		if err := rows.Scan(&u.ConsumerKey, &u.Month, &u.Requests, &u.BillableUnits); err != nil {
			return nil, err
		}
		u.Month = u.Month.UTC()
		out = append(out, u)
	}
	return out, rows.Err()
}

func (s *PgStore) SumAPIConsumerRouteUsage(ctx context.Context, accountID, appID, prefix string, since, until time.Time) ([]APIConsumerRouteTotal, error) {
	if accountID == "" || appID == "" || !until.After(since) {
		return nil, ErrNotFound
	}
	rows, err := s.pool.Query(ctx, `
		select route, coalesce(sum(billable_units), 0)::bigint, count(distinct consumer_key)::bigint
		  from api_consumer_route_usage_minutes
		 where account_id = $1::uuid and app_id = $2::uuid and window_start >= $3 and window_start < $4
		   and left(route, length($5)) = $5
		 group by route`, accountID, appID, since.UTC(), until.UTC(), prefix)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []APIConsumerRouteTotal{}
	for rows.Next() {
		var t APIConsumerRouteTotal
		if err := rows.Scan(&t.Route, &t.BillableUnits, &t.Consumers); err != nil {
			return nil, err
		}
		out = append(out, t)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	sortRouteTotals(out)
	return out, nil
}

func (s *PgStore) ListFinalizedAPIConsumerStatementsForApp(ctx context.Context, accountID, appID string, since, until time.Time) ([]APIConsumerUsageStatement, error) {
	if accountID == "" || appID == "" || !until.After(since) {
		return nil, ErrNotFound
	}
	rows, err := s.pool.Query(ctx,
		`select `+apiConsumerUsageStatementSelectCols+`
		   from api_consumer_usage_statements
		  where account_id = $1::uuid and app_id = $2::uuid and status = 'finalized'
		    and period_start >= $3 and period_start < $4
		  order by period_start, id`, accountID, appID, since.UTC(), until.UTC())
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []APIConsumerUsageStatement{}
	for rows.Next() {
		statement, err := scanAPIConsumerUsageStatementRow(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, statement)
	}
	return out, rows.Err()
}

func (s *PgStore) ListAPIConsumerPlanAssignmentsForApp(ctx context.Context, accountID, appID string) ([]APIConsumerPlanAssignment, error) {
	if accountID == "" || appID == "" {
		return nil, ErrNotFound
	}
	rows, err := s.pool.Query(ctx, `select `+apiConsumerPlanAssignmentCols+` from api_consumer_plan_assignments
		where account_id = $1::uuid and app_id = $2::uuid
		order by effective_from, id`, accountID, appID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []APIConsumerPlanAssignment{}
	for rows.Next() {
		a, err := scanAPIConsumerPlanAssignment(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, a)
	}
	return out, rows.Err()
}

var (
	_ SellerStatsStore = (*MemStore)(nil)
	_ SellerStatsStore = (*PgStore)(nil)
)
