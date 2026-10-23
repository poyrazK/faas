package state

import (
	"context"
	"fmt"
	"net"
	"slices"
	"sort"
	"strconv"
	"strings"
	"time"
)

// ADR-964 sampled edge-rule security events.

// EdgeRuleEventRetention is how long sampled events are kept.
const EdgeRuleEventRetention = 7 * 24 * time.Hour

// Outcomes of kind=waf detection events (ADR-831 amendment 5), alongside
// EdgeRuleHitMatched and EdgeRuleHitLogged.
const (
	EdgeRuleEventDetected = "detected"
	EdgeRuleEventWarned   = "warned"
	EdgeRuleEventBlocked  = "blocked"
)

// Outcomes of kind=challenge events (ADR-831 amendment 7 follow-up): the
// challenge page served, a non-browser request refused, a solution that
// earned a pass, and one that was refused.
const (
	EdgeRuleEventChallenged = "challenged"
	EdgeRuleEventRejected   = "rejected"
	EdgeRuleEventSolved     = "solved"
	EdgeRuleEventFailed     = "failed"
)

// IsEdgeRuleEventOutcome reports whether outcome is a stored event outcome.
func IsEdgeRuleEventOutcome(outcome string) bool {
	switch outcome {
	case EdgeRuleHitMatched, EdgeRuleHitLogged, EdgeRuleEventDetected, EdgeRuleEventWarned, EdgeRuleEventBlocked,
		EdgeRuleEventChallenged, EdgeRuleEventRejected, EdgeRuleEventSolved, EdgeRuleEventFailed:
		return true
	}
	return false
}

// EdgeRuleEvent is one sampled rule match, kind=waf detection or
// kind=challenge outcome. Outcome is EdgeRuleHitMatched, EdgeRuleHitLogged
// or one of the outcomes above; the WAF* fields are set on detections only.
// ID is assigned by the store.
type EdgeRuleEvent struct {
	ID         int64
	RuleID     string
	AppID      string
	OccurredAt time.Time
	Outcome    string
	RequestID  string
	Method     string
	Host       string
	Path       string
	ClientIP   string
	Country    string
	UserAgent  string

	WAFRuleIDs    []int
	WAFCategories []string
	WAFScore      int
}

// EdgeRuleEventQuery selects an app's events newest first. RuleID, Outcome
// and RequestID filter when set; BeforeAt/BeforeID continue a page
// (exclusive).
type EdgeRuleEventQuery struct {
	AppID     string
	RuleID    string
	Outcome   string
	RequestID string
	Since     time.Time
	BeforeAt  time.Time
	BeforeID  int64
	Limit     int
}

// EdgeRuleEventStore is the ADR-964 capability; apid and gatewayd
// type-assert it.
type EdgeRuleEventStore interface {
	RecordEdgeRuleEvents(ctx context.Context, events []EdgeRuleEvent) error
	ListEdgeRuleEvents(ctx context.Context, q EdgeRuleEventQuery) ([]EdgeRuleEvent, error)
	PruneEdgeRuleEvents(ctx context.Context, before time.Time) (int64, error)
}

var (
	_ EdgeRuleEventStore = (*PgStore)(nil)
	_ EdgeRuleEventStore = (*MemStore)(nil)
)

func (s *PgStore) RecordEdgeRuleEvents(ctx context.Context, events []EdgeRuleEvent) error {
	if len(events) == 0 {
		return nil
	}
	n := len(events)
	rules, apps, outcomes := make([]string, n), make([]string, n), make([]string, n)
	at := make([]time.Time, n)
	reqIDs, methods, hosts, paths := make([]string, n), make([]string, n), make([]string, n), make([]string, n)
	ips, countries, agents := make([]*string, n), make([]string, n), make([]string, n)
	// Per-row arrays cannot pass through unnest (it flattens nested arrays),
	// so each row's WAF lists travel as one comma-joined string.
	wafRules, wafCategories, wafScores := make([]string, n), make([]string, n), make([]int32, n)
	for i, e := range events {
		wafRules[i] = joinInts(e.WAFRuleIDs)
		wafCategories[i] = strings.Join(e.WAFCategories, ",")
		wafScores[i] = int32(e.WAFScore)
		rules[i], apps[i], outcomes[i], at[i] = e.RuleID, e.AppID, e.Outcome, e.OccurredAt.UTC()
		reqIDs[i], methods[i], hosts[i], paths[i] = e.RequestID, e.Method, e.Host, e.Path
		countries[i], agents[i] = e.Country, e.UserAgent
		if net.ParseIP(e.ClientIP) != nil {
			ip := e.ClientIP
			ips[i] = &ip
		}
	}
	_, err := s.pool.Exec(ctx, `
		insert into edge_rule_events (rule_id, app_id, outcome, occurred_at, request_id, method, host, path, client_ip, country, user_agent,
		                              waf_rule_ids, waf_categories, waf_score)
		select r, a, o, t, q, m, h, p, ip, cc, ua, string_to_array(wr, ',')::integer[], string_to_array(wc, ','), ws
		from unnest($1::uuid[], $2::uuid[], $3::text[], $4::timestamptz[], $5::text[], $6::text[], $7::text[], $8::text[], $9::inet[], $10::text[], $11::text[],
		            $12::text[], $13::text[], $14::integer[]) as u(r, a, o, t, q, m, h, p, ip, cc, ua, wr, wc, ws)`,
		rules, apps, outcomes, at, reqIDs, methods, hosts, paths, ips, countries, agents, wafRules, wafCategories, wafScores)
	if err != nil {
		return fmt.Errorf("state: record edge-rule events: %w", err)
	}
	return nil
}

func (s *PgStore) ListEdgeRuleEvents(ctx context.Context, q EdgeRuleEventQuery) ([]EdgeRuleEvent, error) {
	var ruleArg, outcomeArg, beforeArg, requestArg any
	if q.RuleID != "" {
		ruleArg = q.RuleID
	}
	if q.RequestID != "" {
		requestArg = q.RequestID
	}
	if q.Outcome != "" {
		outcomeArg = q.Outcome
	}
	if !q.BeforeAt.IsZero() {
		beforeArg = q.BeforeAt.UTC()
	}
	rows, err := s.pool.Query(ctx, `
		select id, rule_id::text, app_id::text, occurred_at, outcome, request_id, method, host, path,
		       coalesce(host(client_ip), ''), country, user_agent, waf_rule_ids, waf_categories, waf_score
		from edge_rule_events
		where app_id = $1
		  and occurred_at >= $2
		  and ($3::uuid is null or rule_id = $3)
		  and ($4::text is null or outcome = $4)
		  and ($5::timestamptz is null or (occurred_at, id) < ($5, $6))
		  and ($8::text is null or request_id = $8)
		order by occurred_at desc, id desc
		limit $7`, q.AppID, q.Since.UTC(), ruleArg, outcomeArg, beforeArg, q.BeforeID, q.Limit, requestArg)
	if err != nil {
		return nil, fmt.Errorf("state: list edge-rule events: %w", err)
	}
	defer rows.Close()
	var out []EdgeRuleEvent
	for rows.Next() {
		var e EdgeRuleEvent
		var wafRules []int32
		var wafScore int32
		if err := rows.Scan(&e.ID, &e.RuleID, &e.AppID, &e.OccurredAt, &e.Outcome, &e.RequestID, &e.Method,
			&e.Host, &e.Path, &e.ClientIP, &e.Country, &e.UserAgent, &wafRules, &e.WAFCategories, &wafScore); err != nil {
			return nil, fmt.Errorf("state: scan edge-rule event: %w", err)
		}
		for _, id := range wafRules {
			e.WAFRuleIDs = append(e.WAFRuleIDs, int(id))
		}
		if len(e.WAFCategories) == 0 {
			e.WAFCategories = nil
		}
		e.WAFScore = int(wafScore)
		out = append(out, e)
	}
	return out, rows.Err()
}

func (s *PgStore) PruneEdgeRuleEvents(ctx context.Context, before time.Time) (int64, error) {
	tag, err := s.pool.Exec(ctx, `delete from edge_rule_events where occurred_at < $1`, before.UTC())
	if err != nil {
		return 0, fmt.Errorf("state: prune edge-rule events: %w", err)
	}
	return tag.RowsAffected(), nil
}

func (m *MemStore) RecordEdgeRuleEvents(_ context.Context, events []EdgeRuleEvent) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, e := range events {
		m.edgeRuleEventSeq++
		e.ID = m.edgeRuleEventSeq
		e.OccurredAt = e.OccurredAt.UTC()
		if net.ParseIP(e.ClientIP) == nil {
			e.ClientIP = ""
		}
		e.WAFRuleIDs, e.WAFCategories = slices.Clone(e.WAFRuleIDs), slices.Clone(e.WAFCategories)
		m.edgeRuleEvents = append(m.edgeRuleEvents, e)
	}
	return nil
}

func edgeRuleEventBefore(e EdgeRuleEvent, at time.Time, id int64) bool {
	return e.OccurredAt.Before(at) || (e.OccurredAt.Equal(at) && e.ID < id)
}

func (m *MemStore) ListEdgeRuleEvents(_ context.Context, q EdgeRuleEventQuery) ([]EdgeRuleEvent, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	var out []EdgeRuleEvent
	for _, e := range m.edgeRuleEvents {
		switch {
		case e.AppID != q.AppID, e.OccurredAt.Before(q.Since),
			q.RuleID != "" && e.RuleID != q.RuleID,
			q.Outcome != "" && e.Outcome != q.Outcome,
			q.RequestID != "" && e.RequestID != q.RequestID,
			!q.BeforeAt.IsZero() && !edgeRuleEventBefore(e, q.BeforeAt.UTC(), q.BeforeID):
			continue
		}
		out = append(out, e)
	}
	sort.Slice(out, func(i, j int) bool { return edgeRuleEventBefore(out[j], out[i].OccurredAt, out[i].ID) })
	if q.Limit > 0 && len(out) > q.Limit {
		out = out[:q.Limit]
	}
	return out, nil
}

func (m *MemStore) PruneEdgeRuleEvents(_ context.Context, before time.Time) (int64, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	kept := m.edgeRuleEvents[:0]
	var n int64
	for _, e := range m.edgeRuleEvents {
		if e.OccurredAt.Before(before) {
			n++
			continue
		}
		kept = append(kept, e)
	}
	m.edgeRuleEvents = kept
	return n, nil
}

// joinInts renders ids as "1,2,3" for string_to_array on the server.
func joinInts(ids []int) string {
	parts := make([]string, len(ids))
	for i, id := range ids {
		parts[i] = strconv.Itoa(id)
	}
	return strings.Join(parts, ",")
}
