package state

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"sort"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
)

// recordEdgeRuleSetVersionLocked mirrors the edge_rules_record_set_version
// trigger: snapshot the app's whole rule set and append it as a new version
// unless it is identical to the latest one. Caller holds m.mu.
func (m *MemStore) recordEdgeRuleSetVersionLocked(appID string) {
	rules := m.appEdgeRulesLocked(appID)
	raw, _ := json.Marshal(edgeRuleSnapshotRows(rules))
	sum := sha256.Sum256(raw)
	digest := hex.EncodeToString(sum[:])
	if m.edgeRuleSetVersions == nil {
		m.edgeRuleSetVersions = map[string][]EdgeRuleSetVersion{}
	}
	versions := m.edgeRuleSetVersions[appID]
	next := 1
	if n := len(versions); n > 0 {
		if versions[n-1].RulesSHA256 == digest {
			return
		}
		next = versions[n-1].Version + 1
	}
	versions = append(versions, EdgeRuleSetVersion{
		AppID: appID, Version: next, RuleCount: len(rules),
		RulesSHA256: digest, CreatedAt: time.Now(), Rules: rules,
	})
	if len(versions) > EdgeRuleSetVersionRetention {
		versions = versions[len(versions)-EdgeRuleSetVersionRetention:]
	}
	m.edgeRuleSetVersions[appID] = versions
}

// appEdgeRulesLocked returns deep-enough copies of the app's rules ordered
// by ID (the snapshot order the SQL function uses).
func (m *MemStore) appEdgeRulesLocked(appID string) []EdgeRule {
	var out []EdgeRule
	for _, r := range m.edgeRules {
		if r.AppID == appID {
			r.MatchHeaders = cloneEdgeRuleMatchHeaders(r.MatchHeaders)
			out = append(out, r)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out
}

func edgeRuleSnapshotRows(rules []EdgeRule) []edgeRuleSnapshotRow {
	rows := make([]edgeRuleSnapshotRow, 0, len(rules))
	for _, r := range rules {
		row := edgeRuleSnapshotRow{
			ID: r.ID, AccountID: r.AccountID, AppID: r.AppID,
			MatchHost: r.MatchHost, MatchPath: r.MatchPath, MatchMethods: r.MatchMethods,
			MatchHeaders: r.MatchHeaders, Priority: r.Priority, Enabled: r.Enabled,
			Kind: string(r.Kind), Action: r.Action, ValidateMode: r.ValidateMode,
			CorsPresetID: r.CorsPresetID, ExpiresAt: r.ExpiresAt, CreatedAt: r.CreatedAt,
			MatchExpr: r.Match, Mode: r.Mode,
		}
		if r.ManifestKey != "" {
			row.ManifestKey = &r.ManifestKey
		}
		if r.Name != "" {
			row.Name = &r.Name
		}
		if r.Description != "" {
			row.Description = &r.Description
		}
		rows = append(rows, row)
	}
	return rows
}

func (m *MemStore) LatestEdgeRuleSetVersion(_ context.Context, appID string) (int, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	versions := m.edgeRuleSetVersions[appID]
	if len(versions) == 0 {
		return 0, nil
	}
	return versions[len(versions)-1].Version, nil
}

func (m *MemStore) ListEdgeRuleSetVersions(_ context.Context, appID string, limit int) ([]EdgeRuleSetVersion, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if limit <= 0 || limit > EdgeRuleSetVersionRetention {
		limit = EdgeRuleSetVersionRetention
	}
	versions := m.edgeRuleSetVersions[appID]
	var out []EdgeRuleSetVersion
	for i := len(versions) - 1; i >= 0 && len(out) < limit; i-- {
		v := versions[i]
		v.Rules = nil
		out = append(out, v)
	}
	return out, nil
}

func (m *MemStore) GetEdgeRuleSetVersion(_ context.Context, appID string, version int) (EdgeRuleSetVersion, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, v := range m.edgeRuleSetVersions[appID] {
		if v.Version == version {
			v.Rules = append([]EdgeRule(nil), v.Rules...)
			return v, nil
		}
	}
	return EdgeRuleSetVersion{}, ErrNotFound
}

func (m *MemStore) RestoreEdgeRuleSetVersion(ctx context.Context, appID string, version int, limits api.Limits) (EdgeRuleSetRestore, error) {
	target, err := m.GetEdgeRuleSetVersion(ctx, appID, version)
	if err != nil {
		return EdgeRuleSetRestore{}, err
	}
	if err := checkEdgeRuleSetQuota(target.Rules, limits); err != nil {
		return EdgeRuleSetRestore{}, err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	seen := map[string]struct{}{}
	var previousHosts []string
	for id, r := range m.edgeRules {
		if r.AppID != appID {
			continue
		}
		if _, ok := seen[r.MatchHost]; !ok {
			seen[r.MatchHost] = struct{}{}
			previousHosts = append(previousHosts, r.MatchHost)
		}
		delete(m.edgeRules, id)
	}
	now := time.Now()
	for _, r := range target.Rules {
		r.UpdatedAt = now
		r.MatchHeaders = cloneEdgeRuleMatchHeaders(r.MatchHeaders)
		m.edgeRules[r.ID] = r
	}
	m.enqueueRoutePolicyChecksLocked(appID)
	m.recordEdgeRuleSetVersionLocked(appID)
	restored := m.appEdgeRulesLocked(appID)
	sort.SliceStable(restored, func(i, j int) bool {
		if restored[i].Priority != restored[j].Priority {
			return restored[i].Priority < restored[j].Priority
		}
		return restored[i].CreatedAt.Before(restored[j].CreatedAt)
	})
	return EdgeRuleSetRestore{Rules: restored, PreviousHosts: previousHosts}, nil
}
