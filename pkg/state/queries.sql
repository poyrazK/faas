-- name: CreateAccount :one
insert into accounts (id, email, plan, status, stripe_customer_id)
values (gen_random_uuid(), $1, $2, $3, null)
returning id, email, plan, status, coalesce(stripe_customer_id, ''), created_at;

-- name: AccountByID :one
select id, email, plan, status, coalesce(stripe_customer_id, ''), created_at
from accounts where id = $1;

-- name: AccountByEmail :one
select id, email, plan, status, coalesce(stripe_customer_id, ''), created_at
from accounts where email = $1;

-- name: AccountByKeyHash :one
select a.id, a.email, a.plan, a.status, coalesce(a.stripe_customer_id, ''), a.created_at
from accounts a
join api_keys k on k.account_id = a.id
where k.key_sha256 = $1;

-- name: UpdateAccountPlan :exec
update accounts set plan = $2 where id = $1;

-- name: UpdateAccountStatus :exec
update accounts set status = $2 where id = $1;

-- name: CreateAPIKey :one
insert into api_keys (account_id, key_sha256, label)
values ($1, $2, $3)
returning id, account_id, key_sha256, coalesce(label, ''), created_at;

-- name: DeleteAPIKey :exec
delete from api_keys where id = $1 and account_id = $2;

-- name: ListAPIKeys :many
select id, account_id, key_sha256, coalesce(label, ''), created_at
from api_keys where account_id = $1 order by created_at desc;

-- name: TouchKeyLastUsed :exec
update api_keys set last_used_at = now() where id = $1;

-- name: CreateApp :one
insert into apps (id, account_id, slug, type, runtime, ram_mb, idle_timeout_s, max_concurrency, status, manifest)
values (gen_random_uuid(), $1, $2, $3, $4, $5, $6, $7, 'active', coalesce($8, '{}'::jsonb))
returning id, account_id, slug, type, coalesce(runtime, ''), ram_mb, coalesce(idle_timeout_s, 0),
          max_concurrency, status, manifest, created_at;

-- name: AppByID :one
select id, account_id, slug, type, coalesce(runtime, ''), ram_mb, coalesce(idle_timeout_s, 0),
       max_concurrency, status, manifest, created_at
from apps where id = $1;

-- name: AppBySlug :one
select id, account_id, slug, type, coalesce(runtime, ''), ram_mb, coalesce(idle_timeout_s, 0),
       max_concurrency, status, manifest, created_at
from apps where slug = $1;

-- name: ListApps :many
select id, account_id, slug, type, coalesce(runtime, ''), ram_mb, coalesce(idle_timeout_s, 0),
       max_concurrency, status, manifest, created_at
from apps where account_id = $1 order by created_at desc;

-- name: CountDeployedApps :one
select count(*) from apps where account_id = $1 and status in ('active', 'evicted_cold');

-- name: UpdateApp :one
update apps set
  ram_mb = coalesce($2, ram_mb),
  idle_timeout_s = case when $3::boolean then $4 else idle_timeout_s end,
  max_concurrency = coalesce($5, max_concurrency),
  status = coalesce($6, status)
where id = $1
returning id, account_id, slug, type, coalesce(runtime, ''), ram_mb, coalesce(idle_timeout_s, 0),
          max_concurrency, status, manifest, created_at;

-- name: SetAppManifest :exec
update apps set manifest = $2 where id = $1;

-- name: DeleteApp :exec
update apps set status = 'deleted' where id = $1;

-- name: CreateDeployment :one
insert into deployments (id, app_id, build_id, image_digest, kind, source_path, source_bytes, handler, log_path, status)
values (gen_random_uuid(), $1, null, $2, $3, $4, $5, $6, $7, 'pending')
returning id, app_id, coalesce(build_id::text, ''), image_digest, kind,
          coalesce(source_path, ''), coalesce(source_bytes, 0),
          coalesce(handler, ''), coalesce(log_path, ''),
          status, coalesce(error, ''), created_at;

-- name: DeploymentByID :one
select id, app_id, coalesce(build_id::text, ''), image_digest, kind,
       coalesce(source_path, ''), coalesce(source_bytes, 0),
       coalesce(handler, ''), coalesce(log_path, ''),
       status, coalesce(error, ''), created_at
from deployments where id = $1;

-- name: LatestDeployment :one
select id, app_id, coalesce(build_id::text, ''), image_digest, kind,
       coalesce(source_path, ''), coalesce(source_bytes, 0),
       coalesce(handler, ''), coalesce(log_path, ''),
       status, coalesce(error, ''), created_at
from deployments where app_id = $1 order by created_at desc limit 1;

-- name: ListDeploymentsForApp :many
select id, app_id, coalesce(build_id::text, ''), image_digest, kind,
       coalesce(source_path, ''), coalesce(source_bytes, 0),
       coalesce(handler, ''), coalesce(log_path, ''),
       status, coalesce(error, ''), created_at
from deployments where app_id = $1 order by created_at desc limit $2 offset $3;

-- name: LatestSupersededDeployment :one
select id, app_id, coalesce(build_id::text, ''), image_digest, kind,
       coalesce(source_path, ''), coalesce(source_bytes, 0),
       coalesce(handler, ''), coalesce(log_path, ''),
       status, coalesce(error, ''), created_at
from deployments
where app_id = $1 and status = 'superseded'
order by created_at desc limit 1;

-- name: UpdateDeploymentStatus :exec
update deployments set status = $2, error = $3 where id = $1;

-- name: SetDeploymentFailed :one
-- ADR-021 (G1, image digest enforcement hardening): durable
-- carrier for the RFC 7807 failure code that imaged writes when a
-- deployment transitions to `failed`. pkg/api.SentinelToCode maps
-- the three puller-side sentinels to the codes pkg/api.CodeImage*
-- (image_not_found / image_egress_denied / image_manifest_invalid)
-- and imaged passes the resulting code as $3 here. The free-text
-- error column ($2) is preserved for debugging. Status is pinned
-- to 'failed' (caller's status argument is ignored — this is a
-- failure-specific helper, not a generic update).
--
-- errcode is omitted in the scan (empty string on success means
-- "no code mapped"; null in the column means "not yet stamped" —
-- both render as "" on the Go side via the coalesce in the SELECT).
update deployments
   set status = 'failed', error = $2, error_code = $3
 where id = $1
returning id, app_id, coalesce(build_id::text, ''), image_digest, kind,
          coalesce(source_path, ''), coalesce(source_bytes, 0),
          coalesce(handler, ''), coalesce(log_path, ''),
          coalesce(rootfs_path, ''), coalesce(rootfs_bytes, 0),
          status, coalesce(error, ''), coalesce(error_code, ''), created_at;

-- name: MarkDeploymentSuperseded :exec
update deployments set status = 'superseded' where id = $1;

-- name: MarkDeploymentLive :exec
update deployments set status = 'live' where id = $1;

-- name: CreateCustomDomain :one
insert into custom_domains (domain, app_id, challenge_token)
values ($1, $2, $3)
returning domain, app_id, challenge_token, verified_at;

-- name: DomainByName :one
select domain, app_id, challenge_token, verified_at
from custom_domains where domain = $1;

-- name: ListDomainsForApp :many
select domain, app_id, challenge_token, verified_at
from custom_domains where app_id = $1 order by domain;

-- name: ListDomainsForAccount :many
select d.domain, d.app_id, d.challenge_token, d.verified_at
from custom_domains d join apps a on a.id = d.app_id
where a.account_id = $1 order by d.domain;

-- name: MarkDomainVerified :exec
update custom_domains set verified_at = now() where domain = $1;

-- name: DeleteCustomDomain :exec
delete from custom_domains where domain = $1;

-- name: CreateCron :one
insert into crons (id, app_id, schedule, path, enabled)
values (gen_random_uuid(), $1, $2, $3, $4)
returning id, app_id, schedule, path, enabled, created_at;

-- name: UpdateCron :one
update crons set
  schedule = coalesce($2, schedule),
  path = coalesce($3, path),
  enabled = coalesce($4, enabled)
where id = $1
returning id, app_id, schedule, path, enabled, created_at;

-- name: DeleteCron :exec
delete from crons where id = $1 and app_id = $2;

-- name: ListCronsForApp :many
select id, app_id, schedule, path, enabled, created_at
from crons where app_id = $1 order by created_at;

-- name: ListEnabledCrons :many
select id, app_id, schedule, path, enabled, created_at
from crons where enabled = true;

-- name: CronByID :one
select id, app_id, schedule, path, enabled, created_at
from crons where id = $1;

-- name: AppendEvent :exec
insert into events (actor, kind, subject, data)
values ($1, $2, $3, $4);

-- name: ListEvents :many
select id, at, actor, kind, subject, data
from events where subject = $1 order by at desc limit $2;

-- name: AppendUsage :exec
insert into usage_minutes (account_id, app_id, instance_id, minute, mb_seconds, requests)
values ($1, $2, $3, $4, $5, $6)
on conflict (instance_id, minute) do update
  set mb_seconds = usage_minutes.mb_seconds + excluded.mb_seconds,
      requests = usage_minutes.requests + excluded.requests;

-- name: UsageByMonth :many
select account_id, app_id, month, mb_seconds, requests
from usage_monthly
where account_id = $1 and month = $2
order by app_id, month;

-- name: CreateInstance :one
insert into instances (id, app_id, deployment_id, state, ram_mb)
values (gen_random_uuid(), $1, $2, $3, $4)
returning id, app_id, deployment_id, state, coalesce(netns, ''), coalesce(guest_uid, 0),
          coalesce(host_ip::text, ''), ram_mb, started_at, last_request_at, parked_at;

-- name: InstanceByID :one
select id, app_id, deployment_id, state, coalesce(netns, ''), coalesce(guest_uid, 0),
       coalesce(host_ip::text, ''), ram_mb, started_at, last_request_at, parked_at
from instances where id = $1;

-- name: ListInstancesForApp :many
select id, app_id, deployment_id, state, coalesce(netns, ''), coalesce(guest_uid, 0),
       coalesce(host_ip::text, ''), ram_mb, started_at, last_request_at, parked_at
from instances where app_id = $1 order by started_at desc;

-- name: UpdateInstanceState :exec
update instances set state = $2 where id = $1;

-- name: CreateBuild :one
insert into builds (id, deployment_id, kind, source_bytes, status, log_path)
values (gen_random_uuid(), $1, $2, $3, 'queued', $4)
returning id, deployment_id, kind, source_bytes, status, failure_class, log_path, started_at, finished_at;

-- name: BuildByID :one
select id, deployment_id, kind, source_bytes, status, failure_class, log_path, started_at, finished_at
from builds where id = $1;

-- name: BuildByDeployment :one
select id, deployment_id, kind, source_bytes, status, failure_class, log_path, started_at, finished_at
from builds where deployment_id = $1 order by started_at desc nulls last limit 1;

-- name: UpdateBuildStatus :exec
update builds set
  status = $2,
  failure_class = $3,
  started_at = case when $4::boolean then now() else started_at end,
  finished_at = case when $5::boolean then now() else finished_at end
where id = $1;
