-- name: ReadDomainPlanTarget :one
SELECT jsonb_build_object(
 'App',a.slug,'AppID',a.id,'AccountID',a.account_id,'OrgID',coalesce(a.org_id::text,''),
 'Status',a.status,'Visibility',a.visibility,'Preview',coalesce(a.preview_of_slug,''),
 'WorkloadClass',a.workload_class,'ExecutionMode',coalesce(a.manifest->>'execution_mode',''),'ProjectID',coalesce(a.project_id::text,''),
 'Environment',coalesce(e.slug,''),'EnvironmentID',coalesce(e.id::text,''),
 'ReleaseID',coalesce(rs.id::text,''),
 'Deployments',coalesce((SELECT jsonb_agg(jsonb_build_object('ID',d.id,'Traffic',d.traffic_percent) ORDER BY d.id)
    FROM deployments d WHERE d.app_id=a.id AND d.status='live' AND d.deleted_at IS NULL AND NOT d.environment_workload_held
     AND ((rs.id IS NOT NULL AND EXISTS(SELECT 1 FROM project_release_members rm WHERE rm.release_id=rs.id AND rm.app_id=a.id AND rm.deployment_id=d.id))
       OR (rs.id IS NULL AND d.scope=sqlc.arg(scope)::text AND d.traffic_percent>0))), '[]'::jsonb)
)::jsonb AS data
FROM apps a LEFT JOIN project_environments e ON e.id=nullif(sqlc.arg(environment_id)::text,'')::uuid
 AND e.account_id=a.account_id AND e.project_id=a.project_id
LEFT JOIN project_release_sets rs ON e.id IS NOT NULL AND rs.project_id=a.project_id AND rs.account_id=a.account_id AND rs.environment_slug=e.slug AND rs.active
WHERE a.id=sqlc.arg(app_id)::text::uuid
 AND (sqlc.arg(environment_id)::text='' OR e.id IS NOT NULL);

-- name: ReadDomainPlanClaim :one
SELECT jsonb_build_object(
 'Claim',jsonb_build_object('Domain',d.domain,'AppID',d.app_id,'EnvironmentID',coalesce(d.environment_id::text,''),
  'ChallengeToken',d.challenge_token,'VerifiedAt',d.verified_at,'CertStatus',d.cert_status,
  'CertExpiresAt',d.cert_expires_at,'CertLastError',coalesce(d.cert_last_error,''),'DNSLastCheckedAt',d.dns_last_checked_at,
  'VerificationNextCheckAt',d.verification_next_check_at,'VerificationExpiresAt',d.verification_expires_at,
  'VerificationAttempts',d.verification_attempts,'BindingVersion',coalesce(d.binding_version::text,'')),
 'Before',jsonb_build_object('app',a.slug,'app_id',a.id,'environment',coalesce(e.slug,''),'environment_id',coalesce(e.id::text,'')),
 'AccountID',a.account_id,'OrgID',coalesce(a.org_id::text,''),
 'Default',EXISTS(SELECT 1 FROM app_default_domains f WHERE f.app_id=a.id AND f.domain=d.domain),
 'TenantOverride',EXISTS(SELECT 1 FROM tenant_hostnames h JOIN tenant_surfaces s ON s.id=h.surface_id
    LEFT JOIN platform_tenants t ON t.id=s.platform_tenant_id
    WHERE h.hostname=d.domain AND h.verified_at IS NOT NULL AND s.status<>'deleted' AND (s.status='active' OR t.status='suspended'))
)::jsonb AS data
FROM custom_domains d JOIN apps a ON a.id=d.app_id
LEFT JOIN project_environments e ON e.id=d.environment_id
WHERE d.domain=sqlc.arg(domain)::text::citext;

-- name: ReadDomainPlanBudgetSubject :one
SELECT (CASE WHEN NOT o.personal_org AND o.resource_ownership_enabled THEN o.id ELSE a.account_id END)::text AS subject
FROM apps a LEFT JOIN orgs o ON o.id=a.org_id WHERE a.id=sqlc.arg(app_id)::text::uuid;

-- name: ReadDomainPlanBudget :many
SELECT d.app_id::text AS app_id, count(*)::bigint AS pending FROM custom_domains d JOIN apps a ON a.id=d.app_id
LEFT JOIN orgs o ON o.id=a.org_id
WHERE a.status<>'deleted' AND d.verified_at IS NULL AND d.verification_expires_at>now()
 AND (CASE WHEN NOT o.personal_org AND o.resource_ownership_enabled THEN o.id ELSE a.account_id END)=sqlc.arg(subject)::text::uuid
GROUP BY d.app_id;

-- name: LockDomainPlanApps :many
SELECT id::text FROM apps WHERE id=ANY(sqlc.arg(ids)::uuid[]) ORDER BY id FOR UPDATE;

-- name: InsertDomainPlanClaim :exec
INSERT INTO custom_domains(domain,app_id,environment_id,challenge_token,binding_version)
VALUES(sqlc.arg(domain)::text::citext,sqlc.arg(app_id)::text::uuid,nullif(sqlc.arg(environment_id)::text,'')::uuid,sqlc.arg(token)::text,gen_random_uuid());

-- name: MoveDomainPlanClaim :execrows
UPDATE custom_domains SET app_id=sqlc.arg(app_id)::text::uuid,
 environment_id=nullif(sqlc.arg(environment_id)::text,'')::uuid,app_id_redirect=NULL,
 binding_version=gen_random_uuid(), dns_last_checked_at=clock_timestamp(),
 verification_next_check_at=clock_timestamp()
WHERE domain=sqlc.arg(domain)::text::citext AND app_id=sqlc.arg(before_app_id)::text::uuid
 AND challenge_token=sqlc.arg(token)::text AND verified_at IS NOT NULL;

-- name: ClearMovedDomainDefault :exec
DELETE FROM app_default_domains WHERE domain=sqlc.arg(domain)::text::citext;
