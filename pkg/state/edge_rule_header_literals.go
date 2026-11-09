package state

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"sort"

	"github.com/onebox-faas/faas/pkg/state/sqlc"
)

// EdgeRuleActionRef is a rule's id and stored action.
type EdgeRuleActionRef struct {
	ID     string
	Action EdgeRuleAction
}

// EdgeRuleSetVersionSnapshot is a stored rule-set version read for an in-place
// rewrite. Cursor orders the scan; raw is the snapshot as stored.
type EdgeRuleSetVersionSnapshot struct {
	Cursor      int64
	AppID       string
	Version     int
	RulesSHA256 string
	Rules       []EdgeRule
	raw         []byte
}

// EdgeRuleHeaderLiteralStore serves the sealing backfill of ADR-20261108110000
// (amendment 2026-11-09): headers rules and rule-set versions that still store
// a request header value as written. The scans return any literal request
// header value; the caller applies the credential-name test.
type EdgeRuleHeaderLiteralStore interface {
	ListEdgeRulesWithRequestHeaderLiterals(ctx context.Context, afterID string, limit int) ([]EdgeRuleActionRef, error)
	ListEdgeRuleSetVersionsWithRequestHeaderLiterals(ctx context.Context, after int64, limit int) ([]EdgeRuleSetVersionSnapshot, error)
	// ReplaceEdgeRuleSetVersionActions replaces the actions of the named rules
	// in v, leaving every other field of the snapshot as stored. It reports
	// false, changing nothing, when the version changed since v was read.
	ReplaceEdgeRuleSetVersionActions(ctx context.Context, v EdgeRuleSetVersionSnapshot, actions map[string]EdgeRuleAction) (bool, error)
}

const zeroUUID = "00000000-0000-0000-0000-000000000000"

func (s *PgStore) ListEdgeRulesWithRequestHeaderLiterals(ctx context.Context, afterID string, limit int) ([]EdgeRuleActionRef, error) {
	if afterID == "" {
		afterID = zeroUUID
	}
	after, err := parsePgUUID(afterID)
	if err != nil {
		return nil, fmt.Errorf("state: edge rule cursor: %w", err)
	}
	rows, err := sqlc.New().ListEdgeRulesWithRequestHeaderLiterals(ctx, s.pool, sqlc.ListEdgeRulesWithRequestHeaderLiteralsParams{AfterID: after, RowLimit: int32(limit)})
	if err != nil {
		return nil, fmt.Errorf("state: list edge rules with header literals: %w", err)
	}
	out := make([]EdgeRuleActionRef, 0, len(rows))
	for _, row := range rows {
		ref := EdgeRuleActionRef{ID: uuidString(row.ID)}
		if err := json.Unmarshal(row.Action, &ref.Action); err != nil {
			return nil, fmt.Errorf("state: decode edge rule %s action: %w", ref.ID, err)
		}
		out = append(out, ref)
	}
	return out, nil
}

func (s *PgStore) ListEdgeRuleSetVersionsWithRequestHeaderLiterals(ctx context.Context, after int64, limit int) ([]EdgeRuleSetVersionSnapshot, error) {
	rows, err := sqlc.New().ListEdgeRuleSetVersionsWithRequestHeaderLiterals(ctx, s.pool, sqlc.ListEdgeRuleSetVersionsWithRequestHeaderLiteralsParams{AfterID: after, RowLimit: int32(limit)})
	if err != nil {
		return nil, fmt.Errorf("state: list edge-rule set versions with header literals: %w", err)
	}
	out := make([]EdgeRuleSetVersionSnapshot, 0, len(rows))
	for _, row := range rows {
		rules, err := decodeEdgeRuleSnapshot(row.Rules)
		if err != nil {
			return nil, err
		}
		out = append(out, EdgeRuleSetVersionSnapshot{
			Cursor: row.ID, AppID: uuidString(row.AppID), Version: int(row.Version),
			RulesSHA256: row.RulesSha256, Rules: rules, raw: row.Rules,
		})
	}
	return out, nil
}

func (s *PgStore) ReplaceEdgeRuleSetVersionActions(ctx context.Context, v EdgeRuleSetVersionSnapshot, actions map[string]EdgeRuleAction) (bool, error) {
	raw, err := replaceSnapshotActions(v.raw, actions)
	if err != nil {
		return false, err
	}
	n, err := sqlc.New().ReplaceEdgeRuleSetVersionRules(ctx, s.pool, sqlc.ReplaceEdgeRuleSetVersionRulesParams{
		Rules: raw, ID: v.Cursor, PreviousSha256: v.RulesSHA256,
	})
	if err != nil {
		return false, fmt.Errorf("state: replace edge-rule set version %s/%d: %w", v.AppID, v.Version, err)
	}
	return n == 1, nil
}

// replaceSnapshotActions swaps the "action" of the named rules in a stored
// snapshot and keeps every other key byte for byte.
func replaceSnapshotActions(raw []byte, actions map[string]EdgeRuleAction) ([]byte, error) {
	var rows []map[string]json.RawMessage
	if err := json.Unmarshal(raw, &rows); err != nil {
		return nil, fmt.Errorf("state: decode edge-rule set snapshot: %w", err)
	}
	for _, row := range rows {
		var id string
		if err := json.Unmarshal(row["id"], &id); err != nil {
			return nil, fmt.Errorf("state: edge-rule set snapshot row id: %w", err)
		}
		action, ok := actions[id]
		if !ok {
			continue
		}
		encoded, err := json.Marshal(action)
		if err != nil {
			return nil, err
		}
		row["action"] = encoded
	}
	return json.Marshal(rows)
}

func requestHeaderLiteral(action EdgeRuleAction) bool {
	if action.Headers == nil {
		return false
	}
	for _, op := range action.Headers.RequestHeaders {
		if op.Value != "" && op.SecretID == "" {
			return true
		}
	}
	return false
}

func (m *MemStore) ListEdgeRulesWithRequestHeaderLiterals(_ context.Context, afterID string, limit int) ([]EdgeRuleActionRef, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	var out []EdgeRuleActionRef
	for id, r := range m.edgeRules {
		if r.Kind == EdgeRuleKindHeaders && id > afterID && requestHeaderLiteral(r.Action) {
			out = append(out, EdgeRuleActionRef{ID: id, Action: r.Action})
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	if len(out) > limit {
		out = out[:limit]
	}
	return out, nil
}

// MemStore versions have no row id; the cursor counts versions in app, then
// version, order.
func (m *MemStore) memVersionSnapshotsLocked() []EdgeRuleSetVersionSnapshot {
	apps := make([]string, 0, len(m.edgeRuleSetVersions))
	for app := range m.edgeRuleSetVersions {
		apps = append(apps, app)
	}
	sort.Strings(apps)
	var out []EdgeRuleSetVersionSnapshot
	for _, app := range apps {
		for _, v := range m.edgeRuleSetVersions[app] {
			out = append(out, EdgeRuleSetVersionSnapshot{
				Cursor: int64(len(out) + 1), AppID: app, Version: v.Version, RulesSHA256: v.RulesSHA256,
				Rules: append([]EdgeRule(nil), v.Rules...),
			})
		}
	}
	return out
}

func (m *MemStore) ListEdgeRuleSetVersionsWithRequestHeaderLiterals(_ context.Context, after int64, limit int) ([]EdgeRuleSetVersionSnapshot, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	var out []EdgeRuleSetVersionSnapshot
	for _, v := range m.memVersionSnapshotsLocked() {
		if v.Cursor <= after {
			continue
		}
		for _, r := range v.Rules {
			if r.Kind == EdgeRuleKindHeaders && requestHeaderLiteral(r.Action) {
				out = append(out, v)
				break
			}
		}
		if len(out) == limit {
			break
		}
	}
	return out, nil
}

func (m *MemStore) ReplaceEdgeRuleSetVersionActions(_ context.Context, v EdgeRuleSetVersionSnapshot, actions map[string]EdgeRuleAction) (bool, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	versions := m.edgeRuleSetVersions[v.AppID]
	for i := range versions {
		if versions[i].Version != v.Version {
			continue
		}
		if versions[i].RulesSHA256 != v.RulesSHA256 {
			return false, nil
		}
		rules := append([]EdgeRule(nil), versions[i].Rules...)
		for j := range rules {
			if action, ok := actions[rules[j].ID]; ok {
				rules[j].Action = action
			}
		}
		raw, _ := json.Marshal(edgeRuleSnapshotRows(rules))
		sum := sha256.Sum256(raw)
		versions[i].Rules, versions[i].RulesSHA256 = rules, hex.EncodeToString(sum[:])
		return true, nil
	}
	return false, nil
}
