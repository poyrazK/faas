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

// OwnedEdgeRuleStore is the capability gatewayd type-asserts to load a host's
// rules for the resource owner of the host (ADR-091 amendment 2026-10-26).
// The gateway applies only that owner's rules, so another tenant's rules
// whose match_host also covers the host ("*", "*.example.com") are never read.
//
// Hunt 24 H24-1: the owner is the host app's resource owner — its org when it
// has one, otherwise its account — not the app's creator account. A company
// app keeps its creator's account_id, so keying by account let a rule written
// on another of that creator's apps (in another org, or their personal
// account, even after they left the org) ride along on this host. The read
// joins edge_rules to apps and matches COALESCE(org_id, account_id).
type OwnedEdgeRuleStore interface {
	// OwnedEdgeRulesForHost returns the enabled, unexpired rules of the apps
	// owned by resourceOwnerID whose match_host covers host, and appID's
	// app-wide ("@app") rules, in evaluation order (priority, created_at, id).
	// appID may be empty. For a personal app the resource owner is its account.
	OwnedEdgeRulesForHost(ctx context.Context, host, resourceOwnerID, appID string) ([]EdgeRule, error)
}

var (
	_ OwnedEdgeRuleStore = (*PgStore)(nil)
	_ OwnedEdgeRuleStore = (*MemStore)(nil)
)

func (s *PgStore) OwnedEdgeRulesForHost(ctx context.Context, host, resourceOwnerID, appID string) ([]EdgeRule, error) {
	rows, err := s.pool.Query(ctx, `
		select `+edgeRuleSelectColsE+` from edge_rules e
		 join apps a on a.id = e.app_id
		 where coalesce(a.org_id::text, a.account_id::text) = $2
		   and e.enabled = true
		   and (e.expires_at is null or e.expires_at > now())
		   and (
		   	e.match_host = $1
		   	or e.match_host = '*'
		   	or $1 like replace(replace(e.match_host, '*', '%'), '?', '_')
		   	or (e.app_id = nullif($3, '')::uuid and lower(e.match_host) = $4)
		   )
		 order by e.priority asc, e.created_at asc, e.id asc
	`, host, resourceOwnerID, appID, api.EdgeRuleAppHosts)
	if err != nil {
		return nil, fmt.Errorf("state: owned edge rules for host %s: %w", host, err)
	}
	defer rows.Close()
	return scanEdgeRules(rows)
}

func (m *MemStore) OwnedEdgeRulesForHost(_ context.Context, host, resourceOwnerID, appID string) ([]EdgeRule, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	var out []EdgeRule
	for _, r := range m.edgeRules {
		if m.edgeRuleResourceOwnerLocked(r) != resourceOwnerID || !r.Enabled || r.EdgeRuleExpired(time.Now()) {
			continue
		}
		appWide := appID != "" && r.AppID == appID && api.IsEdgeRuleAppHosts(r.MatchHost)
		if !appWide && !matchHostPattern(r.MatchHost, host) {
			continue
		}
		r.MatchHeaders = cloneEdgeRuleMatchHeaders(r.MatchHeaders)
		out = append(out, r)
	}
	sort.Slice(out, func(i, j int) bool { return edgeRuleMatchOrderLess(out[i], out[j]) })
	return out, nil
}

// OwnerEdgeRuleSetStore lists a resource owner's whole enabled rule set, so the
// gateway can serve every host the owner owns from one read instead of one
// read per new hostname under a wildcard rule (ADR-091 amendment 2026-10-27).
type OwnerEdgeRuleSetStore interface {
	// OwnerEdgeRules returns the enabled, unexpired rules (every app's, "@app"
	// ones included) of the apps owned by resourceOwnerID, in evaluation order,
	// at most limit of them; complete is false when there are more. The owner
	// is the app's org when it has one, else its account (Hunt 24 H24-1).
	OwnerEdgeRules(ctx context.Context, resourceOwnerID string, limit int) (rules []EdgeRule, complete bool, err error)
}

var (
	_ OwnerEdgeRuleSetStore = (*PgStore)(nil)
	_ OwnerEdgeRuleSetStore = (*MemStore)(nil)
)

func (s *PgStore) OwnerEdgeRules(ctx context.Context, resourceOwnerID string, limit int) ([]EdgeRule, bool, error) {
	if limit <= 0 {
		return nil, false, nil
	}
	rows, err := s.pool.Query(ctx, `
		select `+edgeRuleSelectColsE+` from edge_rules e
		 join apps a on a.id = e.app_id
		 where coalesce(a.org_id::text, a.account_id::text) = $1
		   and e.enabled = true
		   and (e.expires_at is null or e.expires_at > now())
		 order by e.priority asc, e.created_at asc, e.id asc
		 limit $2
	`, resourceOwnerID, limit+1)
	if err != nil {
		return nil, false, fmt.Errorf("state: edge rules of owner %s: %w", resourceOwnerID, err)
	}
	defer rows.Close()
	rules, err := scanEdgeRules(rows)
	if err != nil || len(rules) > limit {
		return nil, false, err
	}
	return rules, true, nil
}

func (m *MemStore) OwnerEdgeRules(_ context.Context, resourceOwnerID string, limit int) ([]EdgeRule, bool, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	var out []EdgeRule
	for _, r := range m.edgeRules {
		if m.edgeRuleResourceOwnerLocked(r) != resourceOwnerID || !r.Enabled || r.EdgeRuleExpired(time.Now()) {
			continue
		}
		r.MatchHeaders = cloneEdgeRuleMatchHeaders(r.MatchHeaders)
		out = append(out, r)
	}
	if limit <= 0 || len(out) > limit {
		return nil, false, nil
	}
	sort.Slice(out, func(i, j int) bool { return edgeRuleMatchOrderLess(out[i], out[j]) })
	return out, true, nil
}

// edgeRuleResourceOwnerLocked is the tenant key the gateway scopes a host's
// rules by: the rule's app's org when it has one, else the app's account. An
// app that has vanished from the store falls back to the rule's own account so
// the rule is never silently attributed to a different tenant.
func (m *MemStore) edgeRuleResourceOwnerLocked(r EdgeRule) string {
	app, ok := m.apps[r.AppID]
	if !ok {
		return r.AccountID
	}
	if app.OrgID != "" {
		return app.OrgID
	}
	return app.AccountID
}

// EdgeRulesCoveringHost filters one owner's rule set (OwnerEdgeRules) to what
// OwnedEdgeRulesForHost returns for host: the unexpired rules whose match_host
// covers host, and appID's "@app" rules, in the set's order.
func EdgeRulesCoveringHost(rules []EdgeRule, host, appID string, now time.Time) []EdgeRule {
	var out []EdgeRule
	for _, r := range rules {
		if r.EdgeRuleExpired(now) {
			continue
		}
		if api.IsEdgeRuleAppHosts(r.MatchHost) {
			if appID == "" || r.AppID != appID {
				continue
			}
		} else if !matchHostPattern(r.MatchHost, host) {
			continue
		}
		out = append(out, r)
	}
	return out
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
