package state

import (
	"context"
	"encoding/json"
	"errors"
	"github.com/jackc/pgx/v5"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
)

const readinessMonitorRolesSQL = `JOIN apps a ON a.id=d.app_id JOIN app_mcp_readiness_policies p ON p.app_id=a.id AND p.scope=d.scope CROSS JOIN LATERAL jsonb_array_elements(p.policy->'roles') AS role(value) WHERE d.status='live' AND d.traffic_percent>0 AND a.status='active' AND a.workload_class NOT IN ('worker','job') AND coalesce(a.manifest->>'execution_mode','') NOT IN ('worker','job') AND a.public_auth_mode<>'internal_only' AND role.value->'monitor'->>'enabled'='true'`

func (s *PgStore) ClaimMCPReadinessMonitors(ctx context.Context) ([]MCPReadinessMonitorJob, error) {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return nil, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	// Discovery is bounded. Existing rows retain their leases across policy changes.
	_, err = tx.Exec(ctx, `INSERT INTO deployment_mcp_readiness_monitors(deployment_id,role) SELECT d.id,role.value->>'name' FROM deployments d `+readinessMonitorRolesSQL+` AND NOT EXISTS(SELECT 1 FROM deployment_mcp_readiness_monitors m WHERE m.deployment_id=d.id AND m.role=role.value->>'name') ORDER BY d.id,role.value->>'name' LIMIT 256 ON CONFLICT DO NOTHING`)
	if err != nil {
		return nil, mapErr(err)
	}
	rows, err := tx.Query(ctx, `SELECT m.deployment_id::text,m.role,a.account_id::text,m.status FROM deployment_mcp_readiness_monitors m JOIN deployments d ON d.id=m.deployment_id `+readinessMonitorRolesSQL+` AND m.role=role.value->>'name' AND (m.lease_until IS NULL OR m.lease_until<=now()) AND (m.next_probe_at<=now() OR m.status->>'policy_revision' IS DISTINCT FROM p.policy->>'revision') ORDER BY m.next_probe_at,m.deployment_id,m.role LIMIT 4 FOR UPDATE OF m SKIP LOCKED`)
	if err != nil {
		return nil, mapErr(err)
	}
	type candidate struct {
		id, role, account string
		data              []byte
	}
	candidates := []candidate{}
	for rows.Next() {
		var c candidate
		if err = rows.Scan(&c.id, &c.role, &c.account, &c.data); err != nil {
			rows.Close()
			return nil, err
		}
		candidates = append(candidates, c)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return nil, err
	}
	out := []MCPReadinessMonitorJob{}
	for _, c := range candidates {
		subject, e := readinessSubjectTx(ctx, tx, c.account, c.id)
		if e != nil {
			return nil, e
		}
		var role api.MCPReadinessRole
		for _, r := range subject.Policy.Roles {
			if r.Name == c.role {
				role = r
				break
			}
		}
		if !monitoringEnabled(role) {
			continue
		}
		var prior api.MCPReadinessMonitorStatus
		if err = json.Unmarshal(c.data, &prior); err != nil {
			return nil, err
		}
		status := monitorStatus(subject, role, prior)
		job := MCPReadinessMonitorJob{Subject: subject, Role: role, Status: status, LeaseID: uuid.NewString(), LeaseUntil: subject.CapturedAt.Add(MCPReadinessMonitorLease)}
		data, e := json.Marshal(status)
		if e != nil {
			return nil, e
		}
		_, err = tx.Exec(ctx, `UPDATE deployment_mcp_readiness_monitors SET status=$3,lease_id=$4,lease_until=$5 WHERE deployment_id=$1 AND role=$2`, c.id, c.role, data, job.LeaseID, job.LeaseUntil)
		if err != nil {
			return nil, err
		}
		out = append(out, job)
	}
	return out, tx.Commit(ctx)
}
func (s *PgStore) CompleteMCPReadinessMonitor(ctx context.Context, job MCPReadinessMonitorJob, e api.MCPReadinessEvidence, reason string) error {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	var data []byte
	var now time.Time
	err = tx.QueryRow(ctx, `SELECT m.status,clock_timestamp() FROM deployment_mcp_readiness_monitors m JOIN deployments d ON d.id=m.deployment_id WHERE m.deployment_id=$1 AND m.role=$2 AND m.lease_id=$3 AND m.lease_until>clock_timestamp() AND d.status='live' AND d.traffic_percent>0 FOR UPDATE OF m`, job.Subject.DeploymentID, job.Role.Name, job.LeaseID).Scan(&data, &now)
	if errors.Is(err, pgx.ErrNoRows) {
		return ErrConflict
	}
	if err != nil {
		return mapErr(err)
	}
	if err = json.Unmarshal(data, &job.Status); err != nil {
		return err
	}
	if !monitoringEnabled(job.Role) || e.Role != job.Role.Name || !validateReadinessEvidence(job.Subject, e) {
		return ErrInvalidArgument
	}
	status, err := completeMonitorStatus(job, e, reason, now)
	if err != nil {
		return err
	}
	if _, err = recordMCPReadinessTx(ctx, tx, job.Subject, e); err != nil {
		return err
	}
	data, err = json.Marshal(status)
	if err != nil {
		return err
	}
	_, err = tx.Exec(ctx, `UPDATE deployment_mcp_readiness_monitors SET status=$3,next_probe_at=$4,lease_id=NULL,lease_until=NULL WHERE deployment_id=$1 AND role=$2`, job.Subject.DeploymentID, job.Role.Name, data, status.NextProbeAt)
	if err != nil {
		return err
	}
	return tx.Commit(ctx)
}
func (s *PgStore) ListMCPReadinessMonitoring(ctx context.Context, account, app, id string, scopes ...string) ([]api.MCPReadinessMonitorStatus, error) {
	if _, err := s.GetMCPReadinessPolicy(ctx, account, app); err != nil {
		return nil, err
	}
	var selectedScope any
	if len(scopes) > 0 {
		if api.ValidateScope(readinessScope(scopes)) != nil {
			return nil, ErrInvalidArgument
		}
		selectedScope = readinessScope(scopes)
	}
	var selected any
	if id != "" {
		selected = id
	}
	rows, err := s.pool.Query(ctx, `SELECT d.id::text,d.scope,p.policy,mcp_readiness_subject(d),role.value,(SELECT m.status FROM deployment_mcp_readiness_monitors m WHERE m.deployment_id=d.id AND m.role=role.value->>'name') FROM deployments d `+readinessMonitorRolesSQL+` AND a.id=$1 AND a.account_id=$2 AND ($3::uuid IS NULL OR d.id=$3::uuid) AND ($4::text IS NULL OR d.scope=$4) ORDER BY d.id,role.value->>'name' LIMIT 1001`, app, account, selected, selectedScope)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []api.MCPReadinessMonitorStatus{}
	for rows.Next() {
		var subject MCPReadinessSubject
		var policyData, roleData, statusData []byte
		err = rows.Scan(&subject.DeploymentID, &subject.Scope, &policyData, &subject.ArtifactSHA256, &roleData, &statusData)
		if err != nil {
			return nil, err
		}
		var role api.MCPReadinessRole
		var prior api.MCPReadinessMonitorStatus
		if err = json.Unmarshal(policyData, &subject.Policy); err != nil {
			return nil, err
		}
		if err = json.Unmarshal(roleData, &role); err != nil {
			return nil, err
		}
		if len(statusData) > 0 {
			if err = json.Unmarshal(statusData, &prior); err != nil {
				return nil, err
			}
		}
		status := monitorStatus(subject, role, prior)
		status.State = api.ReadinessMonitorState(status, time.Now().UTC())
		out = append(out, status)
	}
	if len(out) > 1000 {
		return nil, ErrInvalidArgument
	}
	return out, rows.Err()
}
