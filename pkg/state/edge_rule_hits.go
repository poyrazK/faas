package state

import (
	"context"
	"fmt"
	"time"
)

// ADR-904 per-rule hit counts.

// Edge-rule hit outcomes.
const (
	EdgeRuleHitMatched = "matched" // an enforced rule matched
	EdgeRuleHitLogged  = "logged"  // a log-mode rule matched
)

// EdgeRuleHitCountRetention is how long hourly hit buckets are kept.
const EdgeRuleHitCountRetention = 14 * 24 * time.Hour

// EdgeRuleHit is one aggregated count to add to an hourly bucket.
type EdgeRuleHit struct {
	RuleID  string
	AppID   string
	Bucket  time.Time // truncated to the hour by the store
	Outcome string
	Hits    int64
}

// EdgeRuleHitStats is the per-rule total over a window.
type EdgeRuleHitStats struct {
	RuleID  string
	Matched int64
	Logged  int64
}

// EdgeRuleHitStore is the ADR-904 capability; apid and gatewayd type-assert it.
type EdgeRuleHitStore interface {
	// RecordEdgeRuleHits adds counts to their hourly buckets in one write.
	RecordEdgeRuleHits(ctx context.Context, hits []EdgeRuleHit) error
	// EdgeRuleHitStatsForApp totals each rule's hits since the given time.
	EdgeRuleHitStatsForApp(ctx context.Context, appID string, since time.Time) ([]EdgeRuleHitStats, error)
	// PruneEdgeRuleHitCounts deletes buckets older than before.
	PruneEdgeRuleHitCounts(ctx context.Context, before time.Time) (int64, error)
}

var (
	_ EdgeRuleHitStore = (*PgStore)(nil)
	_ EdgeRuleHitStore = (*MemStore)(nil)
)

type edgeRuleHitKey struct {
	rule, app, outcome string
	bucket             time.Time
}

// aggregateEdgeRuleHits merges counts for the same bucket so a batch upsert
// never touches one row twice.
func aggregateEdgeRuleHits(hits []EdgeRuleHit) map[edgeRuleHitKey]int64 {
	out := make(map[edgeRuleHitKey]int64, len(hits))
	for _, h := range hits {
		if h.Hits <= 0 || h.RuleID == "" {
			continue
		}
		out[edgeRuleHitKey{h.RuleID, h.AppID, h.Outcome, h.Bucket.UTC().Truncate(time.Hour)}] += h.Hits
	}
	return out
}

func (s *PgStore) RecordEdgeRuleHits(ctx context.Context, hits []EdgeRuleHit) error {
	agg := aggregateEdgeRuleHits(hits)
	if len(agg) == 0 {
		return nil
	}
	rules := make([]string, 0, len(agg))
	apps := make([]string, 0, len(agg))
	buckets := make([]time.Time, 0, len(agg))
	outcomes := make([]string, 0, len(agg))
	counts := make([]int64, 0, len(agg))
	for k, n := range agg {
		rules, apps, buckets, outcomes, counts = append(rules, k.rule), append(apps, k.app), append(buckets, k.bucket), append(outcomes, k.outcome), append(counts, n)
	}
	_, err := s.pool.Exec(ctx, `
		insert into edge_rule_hit_counts (rule_id, app_id, bucket_start, outcome, hits)
		select * from unnest($1::uuid[], $2::uuid[], $3::timestamptz[], $4::text[], $5::bigint[])
		on conflict (rule_id, bucket_start, outcome)
		do update set hits = edge_rule_hit_counts.hits + excluded.hits`,
		rules, apps, buckets, outcomes, counts)
	if err != nil {
		return fmt.Errorf("state: record edge-rule hits: %w", err)
	}
	return nil
}

func (s *PgStore) EdgeRuleHitStatsForApp(ctx context.Context, appID string, since time.Time) ([]EdgeRuleHitStats, error) {
	rows, err := s.pool.Query(ctx, `
		select rule_id::text,
		       coalesce(sum(hits) filter (where outcome = 'matched'), 0),
		       coalesce(sum(hits) filter (where outcome = 'logged'), 0)
		from edge_rule_hit_counts
		where app_id = $1 and bucket_start >= date_trunc('hour', $2::timestamptz)
		group by rule_id
		order by rule_id`, appID, since.UTC())
	if err != nil {
		return nil, fmt.Errorf("state: edge-rule hit stats: %w", err)
	}
	defer rows.Close()
	var out []EdgeRuleHitStats
	for rows.Next() {
		var st EdgeRuleHitStats
		if err := rows.Scan(&st.RuleID, &st.Matched, &st.Logged); err != nil {
			return nil, fmt.Errorf("state: scan edge-rule hit stats: %w", err)
		}
		out = append(out, st)
	}
	return out, rows.Err()
}

func (s *PgStore) PruneEdgeRuleHitCounts(ctx context.Context, before time.Time) (int64, error) {
	tag, err := s.pool.Exec(ctx, `delete from edge_rule_hit_counts where bucket_start < $1`, before.UTC())
	if err != nil {
		return 0, fmt.Errorf("state: prune edge-rule hit counts: %w", err)
	}
	return tag.RowsAffected(), nil
}

func (m *MemStore) RecordEdgeRuleHits(_ context.Context, hits []EdgeRuleHit) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.edgeRuleHitCounts == nil {
		m.edgeRuleHitCounts = map[edgeRuleHitKey]int64{}
	}
	for k, n := range aggregateEdgeRuleHits(hits) {
		m.edgeRuleHitCounts[k] += n
	}
	return nil
}

func (m *MemStore) EdgeRuleHitStatsForApp(_ context.Context, appID string, since time.Time) ([]EdgeRuleHitStats, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	from := since.UTC().Truncate(time.Hour)
	byRule := map[string]*EdgeRuleHitStats{}
	for k, n := range m.edgeRuleHitCounts {
		if k.app != appID || k.bucket.Before(from) {
			continue
		}
		st := byRule[k.rule]
		if st == nil {
			st = &EdgeRuleHitStats{RuleID: k.rule}
			byRule[k.rule] = st
		}
		if k.outcome == EdgeRuleHitLogged {
			st.Logged += n
		} else {
			st.Matched += n
		}
	}
	out := make([]EdgeRuleHitStats, 0, len(byRule))
	for _, st := range byRule {
		out = append(out, *st)
	}
	sortEdgeRuleHitStats(out)
	return out, nil
}

func (m *MemStore) PruneEdgeRuleHitCounts(_ context.Context, before time.Time) (int64, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	var n int64
	for k := range m.edgeRuleHitCounts {
		if k.bucket.Before(before.UTC()) {
			delete(m.edgeRuleHitCounts, k)
			n++
		}
	}
	return n, nil
}

func sortEdgeRuleHitStats(stats []EdgeRuleHitStats) {
	for i := 1; i < len(stats); i++ {
		for j := i; j > 0 && stats[j].RuleID < stats[j-1].RuleID; j-- {
			stats[j], stats[j-1] = stats[j-1], stats[j]
		}
	}
}

func edgeRuleModeOrEnforce(mode string) string {
	if mode == "" {
		return EdgeRuleModeEnforce
	}
	return mode
}
