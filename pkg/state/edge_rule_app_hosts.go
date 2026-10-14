package state

import (
	"context"
	"fmt"
	"sort"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
)

// ADR-1191: app-wide edge rules. A rule whose match_host is "@app" covers
// every hostname its app serves; the gateway merges an app's app-wide rules
// into the rule set of each host that resolves to that app.

// AppWideEdgeRuleStore is the capability gatewayd type-asserts to load an
// app's app-wide rules next to a host's own.
type AppWideEdgeRuleStore interface {
	// AppWideEdgeRules returns the app's enabled, unexpired "@app" rules in
	// evaluation order (priority, created_at, id).
	AppWideEdgeRules(ctx context.Context, appID string) ([]EdgeRule, error)
}

var (
	_ AppWideEdgeRuleStore = (*PgStore)(nil)
	_ AppWideEdgeRuleStore = (*MemStore)(nil)
)

func (s *PgStore) AppWideEdgeRules(ctx context.Context, appID string) ([]EdgeRule, error) {
	rows, err := s.pool.Query(ctx, `
		select `+edgeRuleSelectCols+` from edge_rules
		 where app_id = $1
		   and lower(match_host) = $2
		   and enabled = true
		   and (expires_at is null or expires_at > now())
		 order by priority asc, created_at asc, id asc
	`, appID, api.EdgeRuleAppHosts)
	if err != nil {
		return nil, fmt.Errorf("state: app-wide edge rules for app %s: %w", appID, err)
	}
	defer rows.Close()
	return scanEdgeRules(rows)
}

func (m *MemStore) AppWideEdgeRules(_ context.Context, appID string) ([]EdgeRule, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	var out []EdgeRule
	for _, r := range m.edgeRules {
		if r.AppID != appID || !api.IsEdgeRuleAppHosts(r.MatchHost) || !r.Enabled || r.EdgeRuleExpired(time.Now()) {
			continue
		}
		r.MatchHeaders = cloneEdgeRuleMatchHeaders(r.MatchHeaders)
		out = append(out, r)
	}
	sort.Slice(out, func(i, j int) bool { return edgeRuleMatchOrderLess(out[i], out[j]) })
	return out, nil
}

// MergeEdgeRulesInOrder merges two rule slices already in evaluation order
// into one, keeping that order and dropping duplicate ids.
func MergeEdgeRulesInOrder(a, b []EdgeRule) []EdgeRule {
	if len(b) == 0 {
		return a
	}
	if len(a) == 0 {
		return b
	}
	out := make([]EdgeRule, 0, len(a)+len(b))
	seen := make(map[string]struct{}, len(a)+len(b))
	i, j := 0, 0
	for i < len(a) || j < len(b) {
		var next EdgeRule
		if j >= len(b) || (i < len(a) && !edgeRuleMatchOrderLess(b[j], a[i])) {
			next, i = a[i], i+1
		} else {
			next, j = b[j], j+1
		}
		if _, dup := seen[next.ID]; dup {
			continue
		}
		seen[next.ID] = struct{}{}
		out = append(out, next)
	}
	return out
}
