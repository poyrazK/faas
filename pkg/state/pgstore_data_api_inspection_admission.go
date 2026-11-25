package state

import (
	"context"
	"errors"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/onebox-faas/faas/pkg/dataapiinspectioncommand"
)

var _ DataAPIInspectionAdmissionStore = (*PgStore)(nil)

// The generation row serializes app runtime and deployment changes. Binding
// and database revisions detect changes even when values are later restored.
const dataAPIInspectionCurrentSelection = `
 FROM apps a
 JOIN deployments p ON p.app_id=a.id AND p.id=$3::uuid
 JOIN app_runtime_generations g ON g.app_id=a.id
 JOIN managed_postgres_bindings b ON b.id=$5::uuid AND b.app_id=a.id AND b.account_id=a.account_id
 JOIN managed_postgres_databases d ON d.id=$4::uuid AND d.id=b.database_id AND d.account_id=b.database_account_id
 WHERE a.id=$2::uuid AND a.account_id=$1::uuid AND a.status <> 'deleted'
 AND COALESCE(NULLIF(p.scope,''),'default')='default' AND p.status='live' AND p.traffic_percent > 0
 AND p.rootfs_key <> '' AND p.image_digest <> ''

 AND NOT EXISTS (SELECT 1 FROM deployments x WHERE x.app_id=a.id AND x.id<>p.id AND COALESCE(NULLIF(x.scope,''),'default')='default' AND x.status='live' AND x.traffic_percent>0)
 AND b.scope='default' AND b.environment_key='DATABASE_URL' AND b.access='data_api' AND b.state='ready' AND b.deleted_at IS NULL AND b.rotation_previous_generation IS NULL
 AND d.state='ready' AND d.deleted_at IS NULL AND d.desired_generation=d.observed_generation AND d.desired_generation>0`

func validDataAPIInspectionIDs(ids ...string) bool {
	for _, id := range ids {
		if parsed, err := uuid.Parse(id); err != nil || parsed == uuid.Nil || parsed.String() != id {
			return false
		}
	}
	return true
}

func (s *PgStore) ReadDataAPIInspectionFence(ctx context.Context, accountID, appID, deploymentID, databaseID, bindingID string) (DataAPIInspectionFence, error) {
	var fence DataAPIInspectionFence
	if !validDataAPIInspectionIDs(accountID, appID, deploymentID, databaseID, bindingID) {
		return fence, ErrInvalidArgument
	}
	err := s.pool.QueryRow(ctx, `SELECT b.data_api_inspection_revision,d.data_api_inspection_revision,g.generation`+dataAPIInspectionCurrentSelection,
		accountID, appID, deploymentID, databaseID, bindingID).Scan(&fence.BindingRevision, &fence.DatabaseRevision, &fence.RuntimeGeneration)
	if errors.Is(err, pgx.ErrNoRows) {
		return DataAPIInspectionFence{}, ErrNotFound
	}
	if err != nil {
		return DataAPIInspectionFence{}, mapErr(err)
	}
	return fence, nil
}

func (s *PgStore) AdmitDataAPIInspection(ctx context.Context, input DataAPIInspectionAdmission) (string, error) {
	command := []string{dataapiinspectioncommand.Command, input.Table, input.Nonce}
	if !validDataAPIInspectionIDs(input.AccountID, input.AppID, input.DeploymentID, input.DatabaseID, input.BindingID) || !dataapiinspectioncommand.Valid(command, false) || !dataapiinspectioncommand.ValidTrustBundleFingerprint(input.TargetDigest) || !dataapiinspectioncommand.ValidTrustBundleFingerprint(input.TrustDigest) || input.Fence.BindingRevision < 1 || input.Fence.DatabaseRevision < 1 || input.Fence.RuntimeGeneration < 1 || input.CredentialGeneration < 1 || input.CredentialRef == "" || input.ProviderIdentityID == "" || input.DatabaseResourceID == "" || input.BackendID == "" || input.BackendFingerprint == "" || input.BindingUpdatedAt.IsZero() || input.DatabaseUpdatedAt.IsZero() || input.CreatedAt.IsZero() {
		return "", ErrInvalidArgument
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return "", mapErr(err)
	}
	defer tx.Rollback(ctx)
	var fence DataAPIInspectionFence
	var generation, desired, observed int64
	var credentialRef, identity, resource, backend, backendFingerprint string
	var bindingUpdated, databaseUpdated time.Time
	err = tx.QueryRow(ctx, `SELECT b.data_api_inspection_revision,d.data_api_inspection_revision,g.generation,
        b.credential_generation,b.credential_ref,b.provider_identity_id,b.updated_at,
        COALESCE(NULLIF(d.data_resource_id,''),d.provider_resource_id),d.backend_id,d.backend_fingerprint,d.updated_at,d.desired_generation,d.observed_generation`+dataAPIInspectionCurrentSelection+` FOR SHARE OF a,p,g,b,d`,
		input.AccountID, input.AppID, input.DeploymentID, input.DatabaseID, input.BindingID).Scan(&fence.BindingRevision, &fence.DatabaseRevision, &fence.RuntimeGeneration, &generation, &credentialRef, &identity, &bindingUpdated, &resource, &backend, &backendFingerprint, &databaseUpdated, &desired, &observed)
	if errors.Is(err, pgx.ErrNoRows) {
		return "", ErrConflict
	}
	if err != nil {
		return "", mapErr(err)
	}
	if fence != input.Fence || generation != input.CredentialGeneration || credentialRef != input.CredentialRef || identity != input.ProviderIdentityID || resource != input.DatabaseResourceID || backend != input.BackendID || backendFingerprint != input.BackendFingerprint || !bindingUpdated.Equal(input.BindingUpdatedAt) || !databaseUpdated.Equal(input.DatabaseUpdatedAt) || desired != input.DatabaseDesiredGeneration || observed != input.DatabaseObservedGeneration {
		return "", ErrConflict
	}
	var taskID string
	err = tx.QueryRow(ctx, `INSERT INTO app_tasks (account_id,app_id,deployment_id,kind,command,command_shell,deployment_scope,artifact_key,image_digest,timeout_seconds,max_output_bytes,created_at,updated_at)
        SELECT a.account_id,a.id,p.id,'manual',$4,false,'default',p.rootfs_key,p.image_digest,60,65536,$5,$5
        FROM apps a JOIN deployments p ON p.app_id=a.id WHERE a.account_id=$1 AND a.id=$2 AND p.id=$3 RETURNING id::text`, input.AccountID, input.AppID, input.DeploymentID, command, input.CreatedAt).Scan(&taskID)
	if err != nil {
		return "", mapErr(err)
	}
	_, err = tx.Exec(ctx, `INSERT INTO data_api_inspection_admissions(task_id,account_id,app_id,deployment_id,binding_id,database_id,table_name,nonce,binding_revision,database_revision,runtime_generation,credential_generation,target_digest,trust_digest) VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14)`, taskID, input.AccountID, input.AppID, input.DeploymentID, input.BindingID, input.DatabaseID, input.Table, input.Nonce, fence.BindingRevision, fence.DatabaseRevision, fence.RuntimeGeneration, input.CredentialGeneration, input.TargetDigest, input.TrustDigest)
	if err != nil {
		return "", mapErr(err)
	}
	if err = tx.Commit(ctx); err != nil {
		return "", mapErr(err)
	}
	return taskID, nil
}

func (s *PgStore) ReadDataAPIInspectionRecord(ctx context.Context, accountID, appID, taskID string) (DataAPIInspectionRecord, error) {
	var r DataAPIInspectionRecord
	if !validDataAPIInspectionIDs(accountID, appID, taskID) {
		return r, ErrInvalidArgument
	}
	var version *int16
	var finished *time.Time
	var evidenceCurrent bool
	err := s.pool.QueryRow(ctx, `SELECT t.id::text,t.app_id::text,t.deployment_id::text,i.database_id::text,i.binding_id::text,t.command,t.status,t.stdout_tail,t.output_truncated,t.exit_code,t.finished_at,i.credential_generation,i.target_digest,i.trust_digest,
		t.account_id=i.account_id AND t.app_id=i.app_id AND t.deployment_id=i.deployment_id AND t.kind='manual' AND NOT t.command_shell AND t.command=ARRAY['__gregale_data_api_write_inspection_v1__',i.table_name,i.nonce] AND
		b.data_api_inspection_revision=i.binding_revision AND d.data_api_inspection_revision=i.database_revision AND g.generation=i.runtime_generation AND p.status='live' AND p.traffic_percent>0 AND COALESCE(NULLIF(p.scope,''),'default')='default' AND a.status<>'deleted'

        AND NOT EXISTS(SELECT 1 FROM deployments x WHERE x.app_id=a.id AND x.id<>p.id AND COALESCE(NULLIF(x.scope,''),'default')='default' AND x.status='live' AND x.traffic_percent>0),
        e.protocol_version,e.stdout_sha256,COALESCE(e.finished_at=t.finished_at,false)
        FROM data_api_inspection_admissions i JOIN app_tasks t ON t.id=i.task_id JOIN apps a ON a.id=t.app_id JOIN deployments p ON p.id=t.deployment_id
        JOIN managed_postgres_bindings b ON b.id=i.binding_id JOIN managed_postgres_databases d ON d.id=i.database_id JOIN app_runtime_generations g ON g.app_id=a.id
        LEFT JOIN data_api_inspection_executions e ON e.task_id=t.id WHERE t.account_id=$1 AND t.app_id=$2 AND t.id=$3`, accountID, appID, taskID).Scan(&r.TaskID, &r.AppID, &r.DeploymentID, &r.DatabaseID, &r.BindingID, &r.Command, &r.Status, &r.Stdout, &r.OutputTruncated, &r.ExitCode, &finished, &r.CredentialGeneration, &r.TargetDigest, &r.TrustDigest, &r.Current, &version, &r.StdoutSHA256, &evidenceCurrent)
	if errors.Is(err, pgx.ErrNoRows) {
		return DataAPIInspectionRecord{}, ErrNotFound
	}
	if err != nil {
		return DataAPIInspectionRecord{}, mapErr(err)
	}
	r.FinishedAt = finished
	if version != nil && *version > 0 && evidenceCurrent {
		r.ProtocolVersion = uint16(*version)
	}
	return r, nil
}
