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
// rules for the account that owns the host (ADR-091 amendment 2026-10-26).
// The gateway applies only the owner's rules, so another account's rules
// whose match_host also covers the host ("*", "*.example.com") are never
// read, and the read uses edge_rules_account_enabled_idx.
type OwnedEdgeRuleStore interface {
	// OwnedEdgeRulesForHost returns accountID's enabled, unexpired rules whose
	// match_host covers host, and appID's app-wide ("@app") rules, in
	// evaluation order (priority, created_at, id). appID may be empty.
	OwnedEdgeRulesForHost(ctx context.Context, host, accountID, appID string) ([]EdgeRule, error)
}

var (
	_ OwnedEdgeRuleStore = (*PgStore)(nil)
	_ OwnedEdgeRuleStore = (*MemStore)(nil)
)

// The gateway's rule reads skip rules whose app is deleted (ADR-091 amendment
// 2026-10-28). A soft-deleted app keeps its rules so a restore brings them
// back, but a rule on "*", a wildcard or another app's host used to keep
// refusing or rewriting the account's other apps' traffic after its app was
// gone.

// edgeRuleAppDeletedLocked reports whether r's app is soft-deleted. Callers
// hold m.mu.
func (m *MemStore) edgeRuleAppDeletedLocked(r EdgeRule) bool {
	app, ok := m.apps[r.AppID]
	return ok && app.Status == AppDeleted
}

func (s *PgStore) OwnedEdgeRulesForHost(ctx context.Context, host, accountID, appID string) ([]EdgeRule, error) {
	rows, err := s.pool.Query(ctx, `
		select `+edgeRuleSelectCols+` from edge_rules
		 where account_id = $2
		   and enabled = true
		   and (expires_at is null or expires_at > now())
		   and not exists (select 1 from apps a where a.id = edge_rules.app_id and a.status = 'deleted')
		   and (
		   	match_host = $1
		   	or match_host = '*'
		   	or $1 like replace(replace(match_host, '*', '%'), '?', '_')
		   	or (app_id = nullif($3, '')::uuid and lower(match_host) = $4)
		   )
		 order by priority asc, created_at asc, id asc
	`, host, accountID, appID, api.EdgeRuleAppHosts)
	if err != nil {
		return nil, fmt.Errorf("state: owned edge rules for host %s: %w", host, err)
	}
	defer rows.Close()
	return scanEdgeRules(rows)
}

func (m *MemStore) OwnedEdgeRulesForHost(_ context.Context, host, accountID, appID string) ([]EdgeRule, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	var out []EdgeRule
	for _, r := range m.edgeRules {
		if r.AccountID != accountID || !r.Enabled || r.EdgeRuleExpired(time.Now()) || m.edgeRuleAppDeletedLocked(r) {
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

// OwnerEdgeRuleSetStore lists an account's whole enabled rule set, so the
// gateway can serve every host the account owns from one read instead of one
// read per new hostname under a wildcard rule (ADR-091 amendment 2026-10-27).
type OwnerEdgeRuleSetStore interface {
	// OwnerEdgeRules returns accountID's enabled, unexpired rules (every app's,
	// "@app" ones included) in evaluation order, at most limit of them;
	// complete is false when the account has more.
	OwnerEdgeRules(ctx context.Context, accountID string, limit int) (rules []EdgeRule, complete bool, err error)
}

var (
	_ OwnerEdgeRuleSetStore = (*PgStore)(nil)
	_ OwnerEdgeRuleSetStore = (*MemStore)(nil)
)

func (s *PgStore) OwnerEdgeRules(ctx context.Context, accountID string, limit int) ([]EdgeRule, bool, error) {
	if limit <= 0 {
		return nil, false, nil
	}
	rows, err := s.pool.Query(ctx, `
		select `+edgeRuleSelectCols+` from edge_rules
		 where account_id = $1
		   and enabled = true
		   and (expires_at is null or expires_at > now())
		   and not exists (select 1 from apps a where a.id = edge_rules.app_id and a.status = 'deleted')
		 order by priority asc, created_at asc, id asc
		 limit $2
	`, accountID, limit+1)
	if err != nil {
		return nil, false, fmt.Errorf("state: edge rules of account %s: %w", accountID, err)
	}
	defer rows.Close()
	rules, err := scanEdgeRules(rows)
	if err != nil || len(rules) > limit {
		return nil, false, err
	}
	return rules, true, nil
}

func (m *MemStore) OwnerEdgeRules(_ context.Context, accountID string, limit int) ([]EdgeRule, bool, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	var out []EdgeRule
	for _, r := range m.edgeRules {
		if r.AccountID != accountID || !r.Enabled || r.EdgeRuleExpired(time.Now()) || m.edgeRuleAppDeletedLocked(r) {
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
