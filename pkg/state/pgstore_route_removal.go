package state

import (
	"context"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state/sqlc"
)

var _ RouteRemovalStore = (*PgStore)(nil)

func pgRouteRemovalPolicy(ctx context.Context, db sqlc.DBTX, accountID, appID string) (api.RouteRemovalPolicy, error) {
	p := defaultRouteRemovalPolicy(appID)
	var grace, age int64
	var updated time.Time
	err := db.QueryRow(ctx, `select p.mode,p.revision,p.grace_seconds,p.max_approval_age_seconds,coalesce(p.baseline_deployment_id::text,''),p.updated_at
 from app_route_removal_policies p join apps a on a.id=p.app_id and a.account_id=p.account_id where p.app_id=$1 and p.account_id=$2 and a.status<>'deleted'`, appID, accountID).Scan(&p.Mode, &p.Revision, &grace, &age, &p.BaselineDeploymentID, &updated)
	if errors.Is(err, pgx.ErrNoRows) {
		var exists bool
		err = db.QueryRow(ctx, `select exists(select 1 from apps where id=$1 and account_id=$2 and status<>'deleted')`, appID, accountID).Scan(&exists)
		if err != nil {
			return p, err
		}
		if !exists {
			return p, ErrNotFound
		}
		return p, nil
	}
	if err != nil {
		return p, err
	}
	p.GracePeriod = (time.Duration(grace) * time.Second).String()
	p.MaxApprovalAge = (time.Duration(age) * time.Second).String()
	p.UpdatedAt = &updated
	return p, nil
}
func (s *PgStore) GetRouteRemovalPolicy(ctx context.Context, accountID, appID string) (api.RouteRemovalPolicy, error) {
	return pgRouteRemovalPolicy(ctx, s.pool, accountID, appID)
}
func (s *PgStore) SetRouteRemovalPolicy(ctx context.Context, accountID, appID string, r api.SetRouteRemovalPolicyRequest) (api.RouteRemovalPolicy, error) {
	grace, age, err := validateRouteRemovalPolicy(r)
	if err != nil {
		return api.RouteRemovalPolicy{}, err
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return api.RouteRemovalPolicy{}, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	var found int
	err = tx.QueryRow(ctx, `select 1 from apps where id=$1 and account_id=$2 and status<>'deleted' for update`, appID, accountID).Scan(&found)
	if err != nil {
		return api.RouteRemovalPolicy{}, mapErr(err)
	}
	p, err := pgRouteRemovalPolicy(ctx, tx, accountID, appID)
	if err != nil {
		return p, err
	}
	if p.Revision != *r.ExpectedRevision {
		return p, ErrRouteRemovalPolicyRevision
	}
	if p.Revision == 0 {
		rows, err := tx.Query(ctx, `select id::text,traffic_percent from deployments where app_id=$1 and status='live' and traffic_percent>0 and coalesce(nullif(scope,''),'default') IN ('default','prod','production') for update`, appID)
		if err != nil {
			return p, err
		}
		count := 0
		percent := 0
		for rows.Next() {
			count++
			if err = rows.Scan(&p.BaselineDeploymentID, &percent); err != nil {
				rows.Close()
				return p, err
			}
		}
		err = rows.Err()
		rows.Close()
		if err != nil {
			return p, err
		}
		if count > 1 || count == 1 && percent != 100 {
			return p, &RouteRemovalBlockedError{"establish_one_production_baseline_at_100_percent"}
		}
	}
	_, err = tx.Exec(ctx, `insert into app_route_removal_policies(app_id,account_id,mode,revision,grace_seconds,max_approval_age_seconds,baseline_deployment_id)
 values($1,$2,$3,$4,$5,$6,nullif($7,'')::uuid) on conflict(app_id) do update set mode=excluded.mode,revision=excluded.revision,
 grace_seconds=excluded.grace_seconds,max_approval_age_seconds=excluded.max_approval_age_seconds,updated_at=clock_timestamp()`, appID, accountID, r.Mode, p.Revision+1, int64(grace/time.Second), int64(age/time.Second), p.BaselineDeploymentID)
	if err != nil {
		return p, mapErr(err)
	}
	p, err = pgRouteRemovalPolicy(ctx, tx, accountID, appID)
	if err != nil {
		return p, err
	}
	body, _ := json.Marshal(p)
	// History records the authenticated actor in the same transaction as the
	// policy revision. Approval receipts independently retain their approver.
	_, err = tx.Exec(ctx, `insert into route_removal_policy_history(app_id,revision,changed_by,policy) values($1,$2,$3,$4)`, appID, p.Revision, routeRemovalActor(ctx, accountID), body)
	if err != nil {
		return p, err
	}
	return p, tx.Commit(ctx)
}
func pgRouteRemovalContract(ctx context.Context, db sqlc.DBTX, accountID, appID, id string) (RouteRemovalContract, error) {
	var c RouteRemovalContract
	d, err := scanDeploymentWithRootfs(db.QueryRow(ctx, `select `+deploymentSelectColumnsWithRootfs+` from deployments where id=$1 and app_id=$2`, id, appID))
	if err != nil {
		return c, mapErr(err)
	}
	c.Deployment = d
	err = db.QueryRow(ctx, `select doc,encode(doc_sha256,'hex'),truncated,captured_at,updated_at from deployment_openapi_docs where deployment_id=$1 and account_id=$2 and app_id=$3 for share`, id, accountID, appID).Scan(&c.Document, &c.SHA256, &c.Truncated, &c.CapturedAt, &c.UpdatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return c, &RouteRemovalBlockedError{"contract_unavailable"}
	}
	if err != nil {
		return c, err
	}
	if c.Truncated {
		return c, &RouteRemovalBlockedError{"contract_truncated"}
	}
	return c, nil
}
func (s *PgStore) ApproveRouteRemoval(ctx context.Context, accountID, appID, actor string, r api.ApproveRouteRemovalRequest, validator RouteRemovalApprovalValidator) (api.RouteRemovalApproval, error) {
	var a api.RouteRemovalApproval
	if validateRouteRemovalRequest(r) != nil || actor == "" || validator == nil {
		return a, ErrInvalidArgument
	}
	mappings, digest, err := NormalizeRouteRemovalMappings(r.Mappings)
	if err != nil {
		return a, err
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return a, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	var found int
	err = tx.QueryRow(ctx, `select 1 from apps where id=$1 and account_id=$2 and status<>'deleted' for update`, appID, accountID).Scan(&found)
	if err != nil {
		return a, mapErr(err)
	}
	p, err := pgRouteRemovalPolicy(ctx, tx, accountID, appID)
	if err != nil {
		return a, err
	}
	if p.Revision != *r.ExpectedPolicyRevision {
		return a, ErrRouteRemovalPolicyRevision
	}
	if p.BaselineDeploymentID != r.BaselineDeploymentID {
		return a, &RouteRemovalBlockedError{"baseline_changed"}
	}
	b, err := pgRouteRemovalContract(ctx, tx, accountID, appID, r.BaselineDeploymentID)
	if err != nil {
		return a, err
	}
	c, err := pgRouteRemovalContract(ctx, tx, accountID, appID, r.CandidateDeploymentID)
	if err != nil {
		return a, err
	}
	if !routeRemovalProductionScope(b.Deployment.Scope) || !routeRemovalProductionScope(c.Deployment.Scope) || b.Deployment.Status != DeployLive || b.Deployment.TrafficPercent != 100 {
		return a, &RouteRemovalBlockedError{"production_baseline_not_serving_100_percent"}
	}
	if b.SHA256 != r.BaselineContractSHA256 || c.SHA256 != r.CandidateContractSHA256 {
		return a, &RouteRemovalBlockedError{"contract_hash_changed"}
	}
	if err = validator(b, c, mappings); err != nil {
		return a, err
	}
	grace, _ := time.ParseDuration(p.GracePeriod)
	age, _ := time.ParseDuration(p.MaxApprovalAge)
	var now time.Time
	if err = tx.QueryRow(ctx, `select clock_timestamp()`).Scan(&now); err != nil {
		return a, err
	}
	// Exclude the last two minutes so minute-collapsed buckets have settled.
	until := now.Add(-2 * time.Minute).Truncate(time.Minute)
	from := until.Add(-grace)
	if err = sqlc.New().LockRouteRemovalCoverage(ctx, tx, NewPgtypeUUID(uuid.MustParse(appID))); err != nil {
		return a, err
	}
	coverageBody, err := sqlc.New().RouteRemovalCoverageBlockers(ctx, tx, sqlc.RouteRemovalCoverageBlockersParams{
		AppID: NewPgtypeUUID(uuid.MustParse(appID)), WindowFrom: pgtype.Timestamptz{Time: from, Valid: true}, WindowUntil: pgtype.Timestamptz{Time: until, Valid: true},
	})
	if err != nil {
		return a, err
	}
	var coverageBlockers []string
	if err = json.Unmarshal(coverageBody, &coverageBlockers); err != nil {
		return a, err
	}
	if len(coverageBlockers) > 0 {
		return a, &RouteRemovalBlockedError{coverageBlockers[0]}
	}
	var baselineSince time.Time
	err = tx.QueryRow(ctx, `select baseline_since from app_route_removal_policies where app_id=$1`, appID).Scan(&baselineSince)
	if err != nil {
		return a, err
	}
	if baselineSince.After(from) || b.CapturedAt.After(from) {
		readyAt := baselineSince.Truncate(time.Minute).Add(grace + 3*time.Minute)
		if captureReady := b.CapturedAt.Truncate(time.Minute).Add(grace + 3*time.Minute); captureReady.After(readyAt) {
			readyAt = captureReady
		}
		return a, &RouteRemovalBlockedError{fmt.Sprintf("grace_period_not_elapsed_on_observed_baseline; earliest_approval_at=%s", readyAt.UTC().Format(time.RFC3339Nano))}
	}
	// Count every production deployment and every identity before any output
	// caps. A disappeared customer or route cannot be hidden by pagination.
	body, _ := json.Marshal(mappings)
	var active bool
	err = tx.QueryRow(ctx, `select exists(select 1 from request_telemetry rt join deployments d on d.id=rt.deployment_id
 join jsonb_to_recordset($3::jsonb) as m(method text,path text) on rt.method=m.method and rt.route IN (m.path,m.method||' '||m.path)
 where rt.app_id=$1 and rt.account_id=$2 and coalesce(nullif(d.scope,''),'default') IN ('default','prod','production') and rt.received_at>=$4 and rt.count>0)`, appID, accountID, body, from).Scan(&active)
	if err != nil {
		return a, err
	}
	if active {
		return a, &RouteRemovalBlockedError{"old_route_observed"}
	}
	a = api.RouteRemovalApproval{ID: uuid.NewString(), AppID: appID, PolicyRevision: p.Revision, BaselineDeploymentID: b.Deployment.ID, CandidateDeploymentID: c.Deployment.ID, BaselineContractSHA256: b.SHA256, CandidateContractSHA256: c.SHA256, MappingSHA256: digest, Mappings: mappings, ApprovedBy: actor, ApprovedAt: now, ValidUntil: now.Add(age), ObservationFrom: from, ObservationUntil: until, Coverage: "observed_only"}
	receipt, _ := json.Marshal(a)
	baselineSHA, _ := hex.DecodeString(b.SHA256)
	candidateSHA, _ := hex.DecodeString(c.SHA256)
	_, err = tx.Exec(ctx, `insert into route_removal_approvals(id,app_id,account_id,policy_revision,baseline_deployment_id,candidate_deployment_id,baseline_sha256,candidate_sha256,mapping_sha256,mappings,approved_by,approved_at,valid_until,observation_from,observation_until,receipt)
 values($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,$15,$16)`, a.ID, appID, accountID, a.PolicyRevision, a.BaselineDeploymentID, a.CandidateDeploymentID, baselineSHA, candidateSHA, digest, body, actor, a.ApprovedAt, a.ValidUntil, a.ObservationFrom, a.ObservationUntil, receipt)
	if err != nil {
		return a, mapErr(err)
	}
	return a, tx.Commit(ctx)
}
func (s *PgStore) CheckRouteRemoval(ctx context.Context, accountID, appID, id string) (api.RouteRemovalCheck, error) {
	var result api.RouteRemovalCheck
	result.CandidateDeploymentID = id
	tx, err := s.pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.RepeatableRead, AccessMode: pgx.ReadOnly})
	if err != nil {
		return result, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	p, err := pgRouteRemovalPolicy(ctx, tx, accountID, appID)
	if err != nil {
		return result, err
	}
	var found bool
	err = tx.QueryRow(ctx, `select exists(select 1 from deployments where id=$1 and app_id=$2)`, id, appID).Scan(&found)
	if err != nil {
		return result, err
	}
	if !found {
		return result, ErrNotFound
	}
	var body []byte
	err = tx.QueryRow(ctx, `select route_removal_check($1)`, id).Scan(&body)
	if err != nil {
		return result, fmt.Errorf("check route removal: %w", mapErr(err))
	}
	if err = json.Unmarshal(body, &result); err != nil {
		return result, err
	}
	result.Policy = p
	routeRemovalCheckGuidance(&result)
	result.CandidateDeploymentID = id
	return result, tx.Commit(ctx)
}
