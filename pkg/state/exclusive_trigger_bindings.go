package state

import (
	"context"
	"encoding/json"
	"errors"
	"slices"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/exclusivework"
	"github.com/onebox-faas/faas/pkg/workpolicy"
)

func validateExclusiveBindingInput(in *ExclusiveTriggerBinding) error {
	if in.Source != "cron" && in.Source != "inbound_webhook" && in.Source != "broker" && in.Source != "job_schedule" {
		return ErrInvalidArgument
	}
	if in.AppID != "" || in.JobID != "" {
		return ErrInvalidArgument
	}
	if _, err := uuid.Parse(in.TriggerID); err != nil {
		return ErrInvalidArgument
	}
	if in.PlatformTenantID != "" {
		if _, err := uuid.Parse(in.PlatformTenantID); err != nil {
			return ErrInvalidArgument
		}
	}
	if !exclusivework.NamePattern.MatchString(in.PolicyName) || len(in.EquivalenceKey) > api.MaxExclusiveIdentityBytes {
		return ErrInvalidArgument
	}
	if _, err := workpolicy.CanonicalScalar(in.Key); err != nil {
		return ErrInvalidArgument
	}
	return nil
}

func exclusiveOperationBrokerTriggerKind(kind string) bool {
	return kind == "queue" || brokerWorkKind(kind)
}

func exclusiveMemUUIDMapKey(id string) string {
	parsed, err := uuid.Parse(id)
	if err != nil {
		return id
	}
	return strings.ReplaceAll(parsed.String(), "-", "")
}

func cloneExclusiveTriggerBinding(in ExclusiveTriggerBinding) ExclusiveTriggerBinding {
	in.Key = slices.Clone(in.Key)
	return in
}

func (s *MemStore) UpsertExclusiveTriggerBinding(ctx context.Context, in ExclusiveTriggerBinding) (ExclusiveTriggerBinding, error) {
	if err := ctx.Err(); err != nil {
		return ExclusiveTriggerBinding{}, err
	}
	if err := validateExclusiveBindingInput(&in); err != nil {
		return ExclusiveTriggerBinding{}, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	account, ok := s.accounts[exclusiveMemUUIDMapKey(in.AccountID)]
	if !ok || account.Status != AccountActive {
		return ExclusiveTriggerBinding{}, ErrNotFound
	}
	switch in.Source {
	case "cron":
		cron, ok := s.crons[in.TriggerID]
		if !ok {
			return ExclusiveTriggerBinding{}, ErrNotFound
		}
		if len(cron.Command) > 0 || cron.SkipIfRunning {
			return ExclusiveTriggerBinding{}, ErrInvalidArgument
		}
		in.AppID = cron.AppID
	case "inbound_webhook":
		endpoint, ok := s.inboundWebhookEndpoints[in.TriggerID]
		if !ok {
			return ExclusiveTriggerBinding{}, ErrNotFound
		}
		in.AppID = endpoint.AppID
	case "broker":
		trigger, ok := s.triggers[in.TriggerID]
		if !ok || !exclusiveOperationBrokerTriggerKind(trigger.Kind) || canonicalMemUUID(trigger.AccountID.String()) != canonicalMemUUID(in.AccountID) {
			return ExclusiveTriggerBinding{}, ErrNotFound
		}
		in.AppID = trigger.AppID.String()
	case "job_schedule":
		job, ok := s.jobs[in.TriggerID]
		if !ok || canonicalMemUUID(job.AccountID) != canonicalMemUUID(in.AccountID) || job.Status == "deleted" || job.Kind != "recurring" || job.CronSchedule == "" {
			return ExclusiveTriggerBinding{}, ErrNotFound
		}
		in.JobID = job.ID
	}
	if in.Source != "job_schedule" {
		app, ok := s.apps[exclusiveMemUUIDMapKey(in.AppID)]
		if !ok || canonicalMemUUID(app.AccountID) != canonicalMemUUID(in.AccountID) || app.Status == AppDeleted {
			return ExclusiveTriggerBinding{}, ErrNotFound
		}
		in.AppID = app.ID
	}
	policy, ok := s.exclusivePolicies[exclusiveMemUUIDMapKey(in.AccountID)+"\x00"+in.PolicyName]
	if !ok || policy.Retired {
		return ExclusiveTriggerBinding{}, ErrNotFound
	}
	member := false
	if in.Source == "job_schedule" {
		member = slices.ContainsFunc(policy.Policy.MemberJobIDs, func(jobID string) bool { return canonicalMemUUID(jobID) == canonicalMemUUID(in.JobID) })
		if policy.Policy.Scope != "account" || in.PlatformTenantID != "" {
			return ExclusiveTriggerBinding{}, ErrInvalidArgument
		}
	} else {
		for _, appID := range policy.Policy.MemberAppIDs {
			if canonicalMemUUID(appID) == canonicalMemUUID(in.AppID) {
				member = true
				break
			}
		}
	}
	if !member {
		return ExclusiveTriggerBinding{}, ErrNotFound
	}
	if policy.Policy.Contention == "join_existing" && in.EquivalenceKey == "" {
		return ExclusiveTriggerBinding{}, ErrInvalidArgument
	}
	if policy.Policy.Scope == "platform_tenant" {
		tenant, ok := s.platformTenants[in.PlatformTenantID]
		if !ok || tenant.AccountID != in.AccountID {
			return ExclusiveTriggerBinding{}, ErrNotFound
		}
		if tenant.Status != PlatformTenantActive {
			return ExclusiveTriggerBinding{}, ErrPlatformTenantSuspended
		}
		linked := false
		for _, surface := range s.tenantSurfaces {
			if surface.AccountID == in.AccountID && s.platformTenantBySurface[surface.ID] == in.PlatformTenantID &&
				surface.AppID == in.AppID && surface.Status == SurfaceStatusActive {
				linked = true
				break
			}
		}
		if !linked {
			return ExclusiveTriggerBinding{}, ErrNotFound
		}
	} else if in.PlatformTenantID != "" {
		return ExclusiveTriggerBinding{}, ErrInvalidArgument
	}
	now := time.Now().UTC()
	key := in.Source + "\x00" + in.TriggerID
	if old, exists := s.exclusiveTriggerBindings[key]; exists {
		if old.AccountID != in.AccountID {
			return ExclusiveTriggerBinding{}, ErrConflict
		}
		in.CreatedAt = old.CreatedAt
	} else {
		in.CreatedAt = now
	}
	in.UpdatedAt = now
	if s.exclusiveTriggerBindings == nil {
		s.exclusiveTriggerBindings = make(map[string]ExclusiveTriggerBinding)
	}
	s.exclusiveTriggerBindings[key] = cloneExclusiveTriggerBinding(in)
	return cloneExclusiveTriggerBinding(in), nil
}

func (s *MemStore) ExclusiveTriggerBinding(ctx context.Context, account, source, triggerID string) (ExclusiveTriggerBinding, error) {
	if err := ctx.Err(); err != nil {
		return ExclusiveTriggerBinding{}, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	in, ok := s.exclusiveTriggerBindings[source+"\x00"+triggerID]
	if !ok || canonicalMemUUID(in.AccountID) != canonicalMemUUID(account) {
		return ExclusiveTriggerBinding{}, ErrNotFound
	}
	if source == "broker" {
		trigger, exists := s.triggers[triggerID]
		if !exists || canonicalMemUUID(trigger.AccountID.String()) != canonicalMemUUID(account) ||
			canonicalMemUUID(trigger.AppID.String()) != canonicalMemUUID(in.AppID) || !exclusiveOperationBrokerTriggerKind(trigger.Kind) {
			return ExclusiveTriggerBinding{}, ErrNotFound
		}
	}
	if source == "job_schedule" {
		job, exists := s.jobs[triggerID]
		if !exists || canonicalMemUUID(job.AccountID) != canonicalMemUUID(account) || canonicalMemUUID(job.ID) != canonicalMemUUID(in.JobID) || job.Status == "deleted" {
			return ExclusiveTriggerBinding{}, ErrNotFound
		}
	}
	if in.PlatformTenantID != "" {
		tenant, exists := s.platformTenants[in.PlatformTenantID]
		if !exists || canonicalMemUUID(tenant.AccountID) != canonicalMemUUID(account) {
			return ExclusiveTriggerBinding{}, ErrNotFound
		}
		if tenant.Status != PlatformTenantActive {
			return ExclusiveTriggerBinding{}, ErrPlatformTenantSuspended
		}
		linked := false
		for _, surface := range s.tenantSurfaces {
			if canonicalMemUUID(surface.AccountID) == canonicalMemUUID(account) && canonicalMemUUID(surface.AppID) == canonicalMemUUID(in.AppID) &&
				s.platformTenantBySurface[surface.ID] == in.PlatformTenantID && surface.Status == SurfaceStatusActive {
				linked = true
				break
			}
		}
		if !linked {
			return ExclusiveTriggerBinding{}, ErrNotFound
		}
	}
	return cloneExclusiveTriggerBinding(in), nil
}

func (s *MemStore) DeleteExclusiveTriggerBinding(ctx context.Context, account, source, triggerID string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	key := source + "\x00" + triggerID
	in, ok := s.exclusiveTriggerBindings[key]
	if !ok || canonicalMemUUID(in.AccountID) != canonicalMemUUID(account) {
		return ErrNotFound
	}
	delete(s.exclusiveTriggerBindings, key)
	return nil
}

func (s *PgStore) UpsertExclusiveTriggerBinding(ctx context.Context, in ExclusiveTriggerBinding) (ExclusiveTriggerBinding, error) {
	if err := validateExclusiveBindingInput(&in); err != nil {
		return ExclusiveTriggerBinding{}, err
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return ExclusiveTriggerBinding{}, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	var accountStatus string
	if err := tx.QueryRow(ctx, `SELECT status FROM accounts WHERE id=$1 FOR UPDATE`, in.AccountID).Scan(&accountStatus); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return ExclusiveTriggerBinding{}, ErrNotFound
		}
		return ExclusiveTriggerBinding{}, err
	}
	if accountStatus != string(AccountActive) {
		return ExclusiveTriggerBinding{}, ErrNotFound
	}
	switch in.Source {
	case "cron":
		var eligible bool
		err = tx.QueryRow(ctx, `SELECT a.account_id::text, c.app_id::text, cardinality(c.command)=0 AND NOT c.skip_if_running FROM crons c JOIN apps a ON a.id=c.app_id WHERE c.id=$1 AND a.account_id=$2 AND a.status <> 'deleted'`, in.TriggerID, in.AccountID).Scan(&in.AccountID, &in.AppID, &eligible)
		if err == nil && !eligible {
			return ExclusiveTriggerBinding{}, ErrInvalidArgument
		}
	case "inbound_webhook":
		err = tx.QueryRow(ctx, `SELECT account_id::text, app_id::text FROM inbound_webhook_endpoints WHERE id=$1 AND account_id=$2`, in.TriggerID, in.AccountID).Scan(&in.AccountID, &in.AppID)
	case "broker":
		err = tx.QueryRow(ctx, `SELECT t.account_id::text, t.app_id::text
FROM triggers t JOIN apps a ON a.id=t.app_id
WHERE t.id=$1 AND t.account_id=$2 AND t.kind IN ('queue','kafka','nats','redis_streams','sqs_compat','amqp','rabbitmq')
  AND a.status <> 'deleted'`, in.TriggerID, in.AccountID).Scan(&in.AccountID, &in.AppID)
	case "job_schedule":
		err = tx.QueryRow(ctx, `SELECT account_id::text, id::text FROM jobs
		WHERE id=$1 AND account_id=$2 AND status <> 'deleted'
		  AND kind='recurring' AND cron_schedule IS NOT NULL AND cron_schedule <> ''
		FOR UPDATE`, in.TriggerID, in.AccountID).Scan(&in.AccountID, &in.JobID)
	}
	if errors.Is(err, pgx.ErrNoRows) {
		return ExclusiveTriggerBinding{}, ErrNotFound
	}
	if err != nil {
		return ExclusiveTriggerBinding{}, err
	}
	var policyID string
	var config []byte
	var retired bool
	if err := tx.QueryRow(ctx, `SELECT id, configuration, retired FROM exclusive_work_policies WHERE account_id=$1 AND name=$2 FOR SHARE`, in.AccountID, in.PolicyName).Scan(&policyID, &config, &retired); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return ExclusiveTriggerBinding{}, ErrNotFound
		}
		return ExclusiveTriggerBinding{}, err
	}
	var policy exclusivework.Policy
	if err := json.Unmarshal(config, &policy); err != nil {
		return ExclusiveTriggerBinding{}, err
	}
	if in.Source == "job_schedule" {
		if retired || !slices.Contains(policy.MemberJobIDs, in.JobID) {
			return ExclusiveTriggerBinding{}, ErrNotFound
		}
		if policy.Scope != "account" || in.PlatformTenantID != "" {
			return ExclusiveTriggerBinding{}, ErrInvalidArgument
		}
	} else if retired || !slices.Contains(policy.MemberAppIDs, in.AppID) {
		return ExclusiveTriggerBinding{}, ErrNotFound
	}
	if policy.Contention == "join_existing" && in.EquivalenceKey == "" {
		return ExclusiveTriggerBinding{}, ErrInvalidArgument
	}
	if policy.Scope == "platform_tenant" {
		if in.PlatformTenantID == "" {
			return ExclusiveTriggerBinding{}, ErrInvalidArgument
		}
		var status string
		if err := tx.QueryRow(ctx, `SELECT status FROM platform_tenants WHERE account_id=$1 AND id=$2`, in.AccountID, in.PlatformTenantID).Scan(&status); err != nil {
			if errors.Is(err, pgx.ErrNoRows) {
				return ExclusiveTriggerBinding{}, ErrNotFound
			}
			return ExclusiveTriggerBinding{}, err
		}
		if status != string(PlatformTenantActive) {
			return ExclusiveTriggerBinding{}, ErrPlatformTenantSuspended
		}
		var linked bool
		if err := tx.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM tenant_surfaces WHERE account_id=$1 AND app_id=$2 AND platform_tenant_id=$3 AND status='active')`, in.AccountID, in.AppID, in.PlatformTenantID).Scan(&linked); err != nil {
			return ExclusiveTriggerBinding{}, err
		}
		if !linked {
			return ExclusiveTriggerBinding{}, ErrNotFound
		}
	} else if in.PlatformTenantID != "" {
		return ExclusiveTriggerBinding{}, ErrInvalidArgument
	}
	row := tx.QueryRow(ctx, `INSERT INTO exclusive_work_trigger_bindings (source,trigger_id,account_id,app_id,job_id,policy_id,policy_name,platform_tenant_id,business_key,equivalence_key)
VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10)
ON CONFLICT (source,trigger_id) DO UPDATE SET account_id=EXCLUDED.account_id, app_id=EXCLUDED.app_id, job_id=EXCLUDED.job_id, policy_id=EXCLUDED.policy_id,
	policy_name=EXCLUDED.policy_name, platform_tenant_id=EXCLUDED.platform_tenant_id, business_key=EXCLUDED.business_key,
equivalence_key=EXCLUDED.equivalence_key, updated_at=clock_timestamp()
							WHERE exclusive_work_trigger_bindings.account_id=EXCLUDED.account_id
RETURNING created_at,updated_at`, in.Source, in.TriggerID, in.AccountID, nullableUUID(in.AppID), nullableUUID(in.JobID), policyID, in.PolicyName, nullableUUID(in.PlatformTenantID), in.Key, in.EquivalenceKey)
	if err := row.Scan(&in.CreatedAt, &in.UpdatedAt); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return ExclusiveTriggerBinding{}, ErrConflict
		}
		return ExclusiveTriggerBinding{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return ExclusiveTriggerBinding{}, err
	}
	return cloneExclusiveTriggerBinding(in), nil
}

func (s *PgStore) ExclusiveTriggerBinding(ctx context.Context, account, source, triggerID string) (ExclusiveTriggerBinding, error) {
	var in ExclusiveTriggerBinding
	var tenantID *string
	err := s.pool.QueryRow(ctx, `SELECT source,trigger_id::text,account_id::text,coalesce(app_id::text,''),coalesce(job_id::text,''),policy_name,platform_tenant_id::text,business_key,equivalence_key,created_at,updated_at
FROM exclusive_work_trigger_bindings WHERE account_id=$1 AND source=$2 AND trigger_id=$3`, account, source, triggerID).
		Scan(&in.Source, &in.TriggerID, &in.AccountID, &in.AppID, &in.JobID, &in.PolicyName, &tenantID, &in.Key, &in.EquivalenceKey, &in.CreatedAt, &in.UpdatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return ExclusiveTriggerBinding{}, ErrNotFound
	}
	if err != nil {
		return ExclusiveTriggerBinding{}, err
	}
	if source == "broker" {
		var exists bool
		if err := s.pool.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM triggers t JOIN apps a ON a.id=t.app_id
WHERE t.id=$1 AND t.account_id=$2 AND t.app_id=$3
  AND t.kind IN ('queue','kafka','nats','redis_streams','sqs_compat','amqp','rabbitmq')
  AND a.status <> 'deleted')`, triggerID, account, in.AppID).Scan(&exists); err != nil {
			return ExclusiveTriggerBinding{}, err
		}
		if !exists {
			return ExclusiveTriggerBinding{}, ErrNotFound
		}
	}
	if source == "job_schedule" {
		var exists bool
		if err := s.pool.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM jobs WHERE id=$1 AND account_id=$2 AND status <> 'deleted')`, triggerID, account).Scan(&exists); err != nil {
			return ExclusiveTriggerBinding{}, err
		}
		if !exists || in.JobID != triggerID || in.AppID != "" {
			return ExclusiveTriggerBinding{}, ErrNotFound
		}
	}
	if tenantID != nil {
		in.PlatformTenantID = *tenantID
		var status string
		if err := s.pool.QueryRow(ctx, `SELECT status FROM platform_tenants WHERE account_id=$1 AND id=$2`, account, *tenantID).Scan(&status); err != nil {
			if errors.Is(err, pgx.ErrNoRows) {
				return ExclusiveTriggerBinding{}, ErrNotFound
			}
			return ExclusiveTriggerBinding{}, err
		}
		if status != string(PlatformTenantActive) {
			return ExclusiveTriggerBinding{}, ErrPlatformTenantSuspended
		}
		var linked bool
		if err := s.pool.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM tenant_surfaces WHERE account_id=$1 AND app_id=$2 AND platform_tenant_id=$3 AND status='active')`, account, in.AppID, *tenantID).Scan(&linked); err != nil {
			return ExclusiveTriggerBinding{}, err
		}
		if !linked {
			return ExclusiveTriggerBinding{}, ErrNotFound
		}
	}
	return in, nil
}

func (s *PgStore) DeleteExclusiveTriggerBinding(ctx context.Context, account, source, triggerID string) error {
	tag, err := s.pool.Exec(ctx, `DELETE FROM exclusive_work_trigger_bindings WHERE account_id=$1 AND source=$2 AND trigger_id=$3`, account, source, triggerID)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

var _ ExclusiveTriggerBindingStore = (*PgStore)(nil)
var _ ExclusiveTriggerBindingStore = (*MemStore)(nil)
