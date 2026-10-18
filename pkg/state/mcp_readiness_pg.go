package state

import (
	"context"
	"encoding/json"
	"errors"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/onebox-faas/faas/pkg/api"
)

var _ MCPReadinessStore = (*PgStore)(nil)
var _ MCPReadinessStore = (*MemStore)(nil)

func readReadinessPolicy(ctx context.Context, tx interface {
	QueryRow(context.Context, string, ...any) pgx.Row
}, app string, lock bool, scopes ...string) (api.MCPReadinessPolicy, error) {
	q := `SELECT policy FROM app_mcp_readiness_policies WHERE app_id=$1 AND scope=$2`
	if lock {
		q += " FOR SHARE"
	}
	var data []byte
	err := tx.QueryRow(ctx, q, app, readinessScope(scopes)).Scan(&data)
	if errors.Is(err, pgx.ErrNoRows) {
		return defaultMCPReadinessPolicy(readinessScope(scopes)), nil
	}
	if err != nil {
		return api.MCPReadinessPolicy{}, mapErr(err)
	}
	var p api.MCPReadinessPolicy
	err = json.Unmarshal(data, &p)
	p.Scope = readinessScope(scopes)
	return p, err
}
func (s *PgStore) GetMCPReadinessPolicy(ctx context.Context, account, app string, scopes ...string) (api.MCPReadinessPolicy, error) {
	a, err := s.AppByID(ctx, app)
	if err != nil || a.AccountID != account || a.Status == AppDeleted {
		return api.MCPReadinessPolicy{}, ErrNotFound
	}
	if api.ValidateScope(readinessScope(scopes)) != nil {
		return api.MCPReadinessPolicy{}, ErrInvalidArgument
	}
	return readReadinessPolicy(ctx, s.pool, app, false, readinessScope(scopes))
}
func (s *PgStore) SetMCPReadinessPolicy(ctx context.Context, account, app string, r api.SetMCPReadinessPolicyRequest) (api.MCPReadinessPolicy, error) {
	return s.setMCPReadinessPolicy(ctx, account, app, r, "")
}
func (s *PgStore) setMCPReadinessPolicy(ctx context.Context, account, app string, r api.SetMCPReadinessPolicyRequest, restore string) (api.MCPReadinessPolicy, error) {
	if restore == "" && api.ValidateMCPReadinessPolicy(r) != nil {
		return api.MCPReadinessPolicy{}, ErrInvalidArgument
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return api.MCPReadinessPolicy{}, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	// Serialize first creation as well as revision updates on the owned app row.
	var id string
	err = tx.QueryRow(ctx, `SELECT id FROM apps WHERE id=$1 AND account_id=$2 AND status<>'deleted' FOR UPDATE`, app, account).Scan(&id)
	if err != nil {
		return api.MCPReadinessPolicy{}, mapErr(err)
	}
	if _, err = tx.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtextextended('mcp-readiness-policy:'||($1::uuid)::text,0))`, app); err != nil {
		return api.MCPReadinessPolicy{}, mapErr(err)
	}
	if restore != "" {
		old, e := readReadinessHistoryPolicy(ctx, tx, app, api.MCPConnectionScope(r.Scope), restore)
		if e != nil {
			return api.MCPReadinessPolicy{}, e
		}
		r = readinessRestoreRequest(old, r.ExpectedRevision, r.Reason)
		if api.ValidateMCPReadinessPolicy(r) != nil {
			return api.MCPReadinessPolicy{}, ErrInvalidArgument
		}
	}
	current, err := readReadinessPolicy(ctx, tx, app, false, r.Scope)
	if err != nil {
		return current, err
	}
	if current.Revision != r.ExpectedRevision {
		return current, ErrConflict
	}
	var catalog api.MCPCatalogMonitorSnapshot
	for _, role := range r.Roles {
		if role.Monitor.ManagedOAuth() && (r.Enabled || role.Monitor.Enabled) {
			var payload []byte
			e := tx.QueryRow(ctx, `SELECT payload FROM mcp_catalog_monitors WHERE app_id=$1 AND account_id=$2 FOR SHARE`, app, account).Scan(&payload)
			if e != nil {
				return api.MCPReadinessPolicy{}, ErrInvalidArgument
			}
			if e = json.Unmarshal(payload, &catalog); e != nil {
				return api.MCPReadinessPolicy{}, e
			}
			break
		}
	}
	if e := validateReadinessOAuthReferences(catalog, r); e != nil {
		return api.MCPReadinessPolicy{}, e
	}
	p := newMCPReadinessPolicy(account, r)
	p.RestoredFrom = restore
	data, err := json.Marshal(p)
	if err != nil {
		return p, err
	}
	_, err = tx.Exec(ctx, `INSERT INTO app_mcp_readiness_policies(app_id,scope,policy) VALUES($1,$2,$3) ON CONFLICT(app_id,scope) DO UPDATE SET policy=excluded.policy`, app, p.Scope, data)
	if err != nil {
		return p, mapErr(err)
	}
	_, err = tx.Exec(ctx, `INSERT INTO app_mcp_readiness_policy_history(revision,app_id,scope,policy,recorded_at) VALUES($1,$2,$3,$4,$5)`, p.Revision, app, p.Scope, data, p.UpdatedAt)
	if err != nil {
		return p, mapErr(err)
	}
	return p, tx.Commit(ctx)
}
func readinessSubjectTx(ctx context.Context, tx pgx.Tx, account, id string, allowInactive ...bool) (MCPReadinessSubject, error) {
	out := MCPReadinessSubject{AccountID: account, DeploymentID: id}
	err := tx.QueryRow(ctx, `SELECT d.app_id::text,d.scope,clock_timestamp() FROM deployments d JOIN apps a ON a.id=d.app_id WHERE d.id=$1 AND a.account_id=$2 AND a.status<>'deleted' AND ($3 OR d.status='live') FOR SHARE OF d`, id, account, len(allowInactive) > 0 && allowInactive[0]).Scan(&out.AppID, &out.Scope, &out.CapturedAt)
	if err != nil {
		return out, mapErr(err)
	}
	if _, err = tx.Exec(ctx, `SELECT pg_advisory_xact_lock_shared(hashtextextended('mcp-readiness-policy:'||($1::uuid)::text,0))`, out.AppID); err != nil {
		return out, mapErr(err)
	}
	out.Policy, err = readReadinessPolicy(ctx, tx, out.AppID, true, out.Scope)
	if err != nil {
		return out, err
	}
	for _, role := range out.Policy.Roles {
		if role.Monitor.ManagedOAuth() {
			if _, err = tx.Exec(ctx, `SELECT app_id FROM mcp_catalog_monitors WHERE app_id=$1 FOR SHARE`, out.AppID); err != nil {
				return out, mapErr(err)
			}
			break
		}
	}
	err = tx.QueryRow(ctx, `SELECT mcp_readiness_subject(d) FROM deployments d WHERE id=$1`, id).Scan(&out.ArtifactSHA256)
	return out, err
}
func (s *PgStore) MCPReadinessSubject(ctx context.Context, account, id string) (MCPReadinessSubject, error) {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return MCPReadinessSubject{}, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	subject, err := readinessSubjectTx(ctx, tx, account, id)
	if err != nil {
		return subject, err
	}
	return subject, tx.Commit(ctx)
}
func (s *PgStore) RecordMCPReadiness(ctx context.Context, subject MCPReadinessSubject, e api.MCPReadinessEvidence) (api.MCPReadinessEvidence, error) {
	if !validateReadinessEvidence(subject, e) {
		return api.MCPReadinessEvidence{}, ErrInvalidArgument
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return e, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	saved, err := recordMCPReadinessTx(ctx, tx, subject, e)
	if err != nil {
		return saved, err
	}
	return saved, tx.Commit(ctx)
}
func recordMCPReadinessTx(ctx context.Context, tx pgx.Tx, subject MCPReadinessSubject, e api.MCPReadinessEvidence) (api.MCPReadinessEvidence, error) {
	current, err := readinessSubjectTx(ctx, tx, subject.AccountID, subject.DeploymentID)
	if err != nil {
		return e, err
	}
	if current.Policy.Revision != subject.Policy.Revision || current.ArtifactSHA256 != subject.ArtifactSHA256 {
		return e, ErrConflict
	}
	if !validateReadinessEvidence(current, e) {
		return api.MCPReadinessEvidence{}, ErrInvalidArgument
	}

	e = stampReadinessEvidence(subject, e)
	data, err := json.Marshal(e)
	if err != nil {
		return e, err
	}
	tag, err := tx.Exec(ctx, `INSERT INTO deployment_mcp_readiness_evidence(deployment_id,role,policy_revision,artifact_sha256,passed,observed_at,evidence) VALUES($1,$2,$3,$4,$5,$6,$7) ON CONFLICT(deployment_id,role) DO UPDATE SET policy_revision=excluded.policy_revision,artifact_sha256=excluded.artifact_sha256,passed=excluded.passed,observed_at=excluded.observed_at,evidence=excluded.evidence WHERE deployment_mcp_readiness_evidence.observed_at<excluded.observed_at OR (deployment_mcp_readiness_evidence.observed_at=excluded.observed_at AND NOT excluded.passed)`, e.DeploymentID, e.Role, e.PolicyRevision, e.ArtifactSHA256, e.Passed, e.ObservedAt, data)
	if err != nil {
		return e, mapErr(err)
	}
	if tag.RowsAffected() != 1 {
		return e, ErrConflict
	}
	return e, nil
}
func (s *PgStore) ListMCPReadiness(ctx context.Context, account, app, id string, scopes ...string) (api.MCPReadinessSnapshot, error) {
	scope := readinessScope(scopes)
	if api.ValidateScope(scope) != nil {
		return api.MCPReadinessSnapshot{}, ErrInvalidArgument
	}
	if id != "" {
		d, e := s.DeploymentByID(ctx, id)
		if e != nil || d.AppID != app || len(scopes) > 0 && api.MCPConnectionScope(d.Scope) != scope {
			return api.MCPReadinessSnapshot{}, ErrNotFound
		}
		scope = api.MCPConnectionScope(d.Scope)
	}
	p, err := s.GetMCPReadinessPolicy(ctx, account, app, scope)
	if err != nil {
		return api.MCPReadinessSnapshot{}, err
	}
	out := api.MCPReadinessSnapshot{Policy: p, Evidence: []api.MCPReadinessEvidence{}, Overrides: []api.MCPReadinessOverride{}}
	var selected any
	if id != "" {
		selected = id
	}
	rows, err := s.pool.Query(ctx, `SELECT e.evidence FROM deployment_mcp_readiness_evidence e JOIN deployments d ON d.id=e.deployment_id WHERE d.app_id=$1 AND d.scope=$3 AND ($2::uuid IS NULL OR d.id=$2::uuid) ORDER BY e.observed_at DESC,e.deployment_id,e.role LIMIT 50`, app, selected, scope)
	if err != nil {
		return out, err
	}
	for rows.Next() {
		var data []byte
		if err = rows.Scan(&data); err != nil {
			rows.Close()
			return out, err
		}
		var e api.MCPReadinessEvidence
		if err = json.Unmarshal(data, &e); err != nil {
			rows.Close()
			return out, err
		}
		out.Evidence = append(out.Evidence, e)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return out, err
	}
	rows, err = s.pool.Query(ctx, `SELECT o.id::text,o.deployment_id::text,o.policy_revision::text,o.artifact_sha256,o.reason,o.created_by::text,o.created_at,o.expires_at,o.used_at FROM deployment_mcp_readiness_overrides o JOIN deployments d ON d.id=o.deployment_id WHERE d.app_id=$1 AND d.scope=$3 AND ($2::uuid IS NULL OR d.id=$2::uuid) ORDER BY o.created_at DESC,o.id DESC LIMIT 50`, app, selected, scope)
	if err != nil {
		return out, err
	}
	defer rows.Close()
	for rows.Next() {
		var o api.MCPReadinessOverride
		if err = rows.Scan(&o.ID, &o.DeploymentID, &o.PolicyRevision, &o.ArtifactSHA256, &o.Reason, &o.CreatedBy, &o.CreatedAt, &o.ExpiresAt, &o.UsedAt); err != nil {
			return out, err
		}
		out.Overrides = append(out.Overrides, o)
	}
	return out, rows.Err()
}
func (s *PgStore) CreateMCPReadinessOverride(ctx context.Context, account, id string, r api.MCPReadinessOverrideRequest) (api.MCPReadinessOverride, error) {
	if len(r.Reason) < 1 || len(r.Reason) > 512 {
		return api.MCPReadinessOverride{}, ErrInvalidArgument
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return api.MCPReadinessOverride{}, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	// Serialize pending approvals so an HTTP retry cannot stack two overrides.
	if _, err = tx.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtextextended('mcp-readiness-override:'||($1::uuid)::text,0))`, id); err != nil {
		return api.MCPReadinessOverride{}, mapErr(err)
	}
	subject, err := readinessSubjectTx(ctx, tx, account, id, true)
	if err != nil {
		return api.MCPReadinessOverride{}, err
	}
	if !subject.Policy.Enabled || subject.Policy.Revision != r.ExpectedRevision {
		return api.MCPReadinessOverride{}, ErrConflict
	}
	var pending bool
	if err = tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM deployment_mcp_readiness_overrides WHERE deployment_id=$1 AND policy_revision=$2 AND artifact_sha256=$3 AND used_at IS NULL AND expires_at>clock_timestamp())`, id, subject.Policy.Revision, subject.ArtifactSHA256).Scan(&pending); err != nil {
		return api.MCPReadinessOverride{}, mapErr(err)
	}
	if pending {
		return api.MCPReadinessOverride{}, ErrConflict
	}
	o := api.MCPReadinessOverride{ID: uuid.NewString(), DeploymentID: id, PolicyRevision: subject.Policy.Revision, ArtifactSHA256: subject.ArtifactSHA256, Reason: r.Reason, CreatedBy: account, CreatedAt: subject.CapturedAt, ExpiresAt: subject.CapturedAt.Add(5 * time.Minute)}
	_, err = tx.Exec(ctx, `INSERT INTO deployment_mcp_readiness_overrides(id,deployment_id,policy_revision,artifact_sha256,reason,created_by,created_at,expires_at) VALUES($1,$2,$3,$4,$5,$6,$7,$8)`, o.ID, id, o.PolicyRevision, o.ArtifactSHA256, o.Reason, account, o.CreatedAt, o.ExpiresAt)
	if err != nil {
		return o, mapErr(err)
	}
	return o, tx.Commit(ctx)
}
