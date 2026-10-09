package state

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgerrcode"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"

	"github.com/onebox-faas/faas/pkg/api"
)

// EdgeRuleSetVersionRetention is how many versions per app the store keeps
// (ADR-732 §2). The Postgres trigger prunes to the same bound.
const EdgeRuleSetVersionRetention = 100

// EdgeRuleSetVersion is one recorded state of an app's whole edge-rule set.
// Rules is populated only by GetEdgeRuleSetVersion.
type EdgeRuleSetVersion struct {
	AppID       string
	Version     int
	RuleCount   int
	RulesSHA256 string
	CreatedAt   time.Time
	Rules       []EdgeRule
}

// EdgeRuleSetRestore reports a rollback: the rules now in force and the
// match_host patterns of the replaced set (the convergence fence and cache
// invalidation must cover both).
type EdgeRuleSetRestore struct {
	Rules         []EdgeRule
	PreviousHosts []string
}

// ErrEdgeRuleSetVersionReference is returned when a version cannot be
// restored because it references a CORS preset that no longer exists.
var ErrEdgeRuleSetVersionReference = errors.New("state: edge-rule set version references a deleted cors preset")

// EdgeRuleSetVersionStore is the ADR-732 versioning capability. It is kept
// out of Store so small test stores need not grow; apid type-asserts it.
type EdgeRuleSetVersionStore interface {
	// LatestEdgeRuleSetVersion returns the app's newest version, 0 if none.
	LatestEdgeRuleSetVersion(ctx context.Context, appID string) (int, error)
	// ListEdgeRuleSetVersions returns versions newest first, without rules.
	ListEdgeRuleSetVersions(ctx context.Context, appID string, limit int) ([]EdgeRuleSetVersion, error)
	// GetEdgeRuleSetVersion returns one version with its rules, or ErrNotFound.
	GetEdgeRuleSetVersion(ctx context.Context, appID string, version int) (EdgeRuleSetVersion, error)
	// RestoreEdgeRuleSetVersion atomically replaces the app's rules with the
	// version's, preserving rule IDs. It fails with *EdgeRuleQuotaError when
	// the restored set exceeds limits, ErrNotFound for an unknown version or
	// app, and ErrEdgeRuleSetVersionReference for a dangling preset.
	RestoreEdgeRuleSetVersion(ctx context.Context, appID string, version int, limits api.Limits) (EdgeRuleSetRestore, error)
}

var (
	_ EdgeRuleSetVersionStore = (*PgStore)(nil)
	_ EdgeRuleSetVersionStore = (*MemStore)(nil)
)

// edgeRuleSnapshotRow is the per-rule JSON shape the edge_rule_set_snapshot
// SQL function emits (to_jsonb of the selected edge_rules columns).
type edgeRuleSnapshotRow struct {
	ID           string                 `json:"id"`
	AccountID    string                 `json:"account_id"`
	AppID        string                 `json:"app_id"`
	MatchHost    string                 `json:"match_host"`
	MatchPath    string                 `json:"match_path"`
	MatchMethods []string               `json:"match_methods"`
	MatchHeaders map[string]string      `json:"match_headers"`
	Priority     int                    `json:"priority"`
	Enabled      bool                   `json:"enabled"`
	Kind         string                 `json:"kind"`
	Action       EdgeRuleAction         `json:"action"`
	ValidateMode string                 `json:"validate_mode"`
	CorsPresetID *string                `json:"cors_preset_id"`
	ManifestKey  *string                `json:"manifest_key"`
	Name         *string                `json:"name"`
	Description  *string                `json:"description"`
	ExpiresAt    *time.Time             `json:"expires_at"`
	CreatedAt    time.Time              `json:"created_at"`
	MatchExpr    *api.EdgeRuleMatchExpr `json:"match_expr"`
}

func (r edgeRuleSnapshotRow) rule() EdgeRule {
	out := EdgeRule{
		ID: r.ID, AccountID: r.AccountID, AppID: r.AppID,
		MatchHost: r.MatchHost, MatchPath: r.MatchPath, MatchMethods: r.MatchMethods,
		MatchHeaders: r.MatchHeaders, Priority: r.Priority, Enabled: r.Enabled,
		Kind: EdgeRuleKind(r.Kind), Action: r.Action, ValidateMode: r.ValidateMode,
		CorsPresetID: r.CorsPresetID, ExpiresAt: r.ExpiresAt, CreatedAt: r.CreatedAt,
		Match: r.MatchExpr,
	}
	if r.ManifestKey != nil {
		out.ManifestKey = *r.ManifestKey
	}
	if r.Name != nil {
		out.Name = *r.Name
	}
	if r.Description != nil {
		out.Description = *r.Description
	}
	return out
}

func decodeEdgeRuleSnapshot(raw []byte) ([]EdgeRule, error) {
	var rows []edgeRuleSnapshotRow
	if err := json.Unmarshal(raw, &rows); err != nil {
		return nil, fmt.Errorf("state: decode edge-rule set snapshot: %w", err)
	}
	out := make([]EdgeRule, 0, len(rows))
	for _, row := range rows {
		out = append(out, row.rule())
	}
	return out, nil
}

// checkEdgeRuleSetQuota applies the same caps CreateEdgeRuleIfUnderQuota
// enforces one rule at a time to a whole restored set: a plan downgrade
// since the version was recorded must not be bypassed by a rollback.
func checkEdgeRuleSetQuota(rules []EdgeRule, limits api.Limits) error {
	if len(rules) > limits.EdgeRulesPerApp {
		return &EdgeRuleQuotaError{Limit: limits.EdgeRulesPerApp, Observed: len(rules)}
	}
	perKind := map[EdgeRuleKind]int{}
	for _, r := range rules {
		perKind[r.Kind]++
	}
	for _, c := range []struct {
		kind  EdgeRuleKind
		limit int
	}{
		{EdgeRuleKindGeo, limits.EdgeRulesGeoPerApp},
		{EdgeRuleKindThrottle, limits.EdgeRulesThrottlePerApp},
	} {
		if c.limit > 0 && perKind[c.kind] > c.limit {
			return &EdgeRuleQuotaError{Limit: c.limit, Observed: perKind[c.kind], Kind: string(c.kind), PerKind: true, PerAppOnly: true}
		}
	}
	return nil
}

func (s *PgStore) LatestEdgeRuleSetVersion(ctx context.Context, appID string) (int, error) {
	var version int
	if err := s.pool.QueryRow(ctx,
		`select coalesce(max(version), 0) from edge_rule_set_versions where app_id = $1`, appID,
	).Scan(&version); err != nil {
		return 0, fmt.Errorf("state: latest edge-rule set version: %w", err)
	}
	return version, nil
}

func (s *PgStore) ListEdgeRuleSetVersions(ctx context.Context, appID string, limit int) ([]EdgeRuleSetVersion, error) {
	if limit <= 0 || limit > EdgeRuleSetVersionRetention {
		limit = EdgeRuleSetVersionRetention
	}
	rows, err := s.pool.Query(ctx, `
		select app_id::text, version, rule_count, rules_sha256, created_at
		from edge_rule_set_versions
		where app_id = $1
		order by version desc
		limit $2`, appID, limit)
	if err != nil {
		return nil, fmt.Errorf("state: list edge-rule set versions: %w", err)
	}
	defer rows.Close()
	var out []EdgeRuleSetVersion
	for rows.Next() {
		var v EdgeRuleSetVersion
		if err := rows.Scan(&v.AppID, &v.Version, &v.RuleCount, &v.RulesSHA256, &v.CreatedAt); err != nil {
			return nil, fmt.Errorf("state: scan edge-rule set version: %w", err)
		}
		out = append(out, v)
	}
	return out, rows.Err()
}

func (s *PgStore) GetEdgeRuleSetVersion(ctx context.Context, appID string, version int) (EdgeRuleSetVersion, error) {
	return getEdgeRuleSetVersion(ctx, s.pool, appID, version)
}

type edgeRuleSetVersionQuerier interface {
	QueryRow(ctx context.Context, sql string, args ...any) pgx.Row
}

func getEdgeRuleSetVersion(ctx context.Context, q edgeRuleSetVersionQuerier, appID string, version int) (EdgeRuleSetVersion, error) {
	var (
		v   EdgeRuleSetVersion
		raw []byte
	)
	err := q.QueryRow(ctx, `
		select app_id::text, version, rule_count, rules_sha256, created_at, rules
		from edge_rule_set_versions
		where app_id = $1 and version = $2`, appID, version,
	).Scan(&v.AppID, &v.Version, &v.RuleCount, &v.RulesSHA256, &v.CreatedAt, &raw)
	if errors.Is(err, pgx.ErrNoRows) {
		return EdgeRuleSetVersion{}, ErrNotFound
	}
	if err != nil {
		return EdgeRuleSetVersion{}, fmt.Errorf("state: get edge-rule set version: %w", err)
	}
	if v.Rules, err = decodeEdgeRuleSnapshot(raw); err != nil {
		return EdgeRuleSetVersion{}, err
	}
	return v, nil
}

func (s *PgStore) RestoreEdgeRuleSetVersion(ctx context.Context, appID string, version int, limits api.Limits) (EdgeRuleSetRestore, error) {
	tx, err := s.pool.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return EdgeRuleSetRestore{}, fmt.Errorf("state: begin tx: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }() //nolint:errcheck // no-op after Commit

	// Same app-row lock CreateEdgeRuleIfUnderQuota takes, so a concurrent
	// create cannot slip a rule in between the delete and the insert.
	var locked int
	if err := tx.QueryRow(ctx,
		`select 1 from apps where id = $1 and status <> 'deleted' for update`, appID,
	).Scan(&locked); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return EdgeRuleSetRestore{}, ErrNotFound
		}
		return EdgeRuleSetRestore{}, fmt.Errorf("state: lock app %s: %w", appID, err)
	}
	target, err := getEdgeRuleSetVersion(ctx, tx, appID, version)
	if err != nil {
		return EdgeRuleSetRestore{}, err
	}
	if err := checkEdgeRuleSetQuota(target.Rules, limits); err != nil {
		return EdgeRuleSetRestore{}, err
	}
	var previousHosts []string
	if err := tx.QueryRow(ctx,
		`select coalesce(array_agg(distinct match_host), '{}') from edge_rules where app_id = $1`, appID,
	).Scan(&previousHosts); err != nil {
		return EdgeRuleSetRestore{}, fmt.Errorf("state: read current edge-rule hosts: %w", err)
	}
	if _, err := tx.Exec(ctx, `delete from edge_rules where app_id = $1`, appID); err != nil {
		return EdgeRuleSetRestore{}, fmt.Errorf("state: clear edge rules for restore: %w", err)
	}
	if _, err := tx.Exec(ctx, `
		insert into edge_rules (
			id, account_id, app_id, match_host, match_path, match_methods,
			match_headers, priority, enabled, kind, action, validate_mode,
			cors_preset_id, manifest_key, name, description, expires_at, created_at,
			match_expr
		)
		select id, account_id, app_id, match_host, match_path, match_methods,
		       match_headers, priority, enabled, kind, action, validate_mode,
		       cors_preset_id, manifest_key, name, description, expires_at, created_at,
		       match_expr
		from jsonb_populate_recordset(null::edge_rules,
			(select rules from edge_rule_set_versions where app_id = $1 and version = $2))`,
		appID, version,
	); err != nil {
		var pgErr *pgconn.PgError
		if errors.As(err, &pgErr) && pgErr.Code == pgerrcode.ForeignKeyViolation {
			return EdgeRuleSetRestore{}, ErrEdgeRuleSetVersionReference
		}
		return EdgeRuleSetRestore{}, fmt.Errorf("state: restore edge rules: %w", err)
	}
	rows, err := tx.Query(ctx,
		`select `+edgeRuleSelectCols+` from edge_rules where app_id = $1 order by priority asc, created_at asc, id asc`, appID)
	if err != nil {
		return EdgeRuleSetRestore{}, fmt.Errorf("state: read restored edge rules: %w", err)
	}
	restored, err := scanEdgeRules(rows)
	rows.Close()
	if err != nil {
		return EdgeRuleSetRestore{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return EdgeRuleSetRestore{}, fmt.Errorf("state: commit edge-rule restore: %w", err)
	}
	return EdgeRuleSetRestore{Rules: restored, PreviousHosts: previousHosts}, nil
}
