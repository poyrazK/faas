-- name: CreateAccount :one
insert into accounts (id, email, plan, status, provider_customer_id)
values (gen_random_uuid(), $1, $2, $3, null)
returning id, email, plan, status, coalesce(provider_customer_id, ''), created_at;

-- name: AccountByID :one
select id, email, plan, status, coalesce(provider_customer_id, ''), created_at
from accounts where id = $1;

-- name: AccountsByIDs :many
select id, email, plan, status, coalesce(provider_customer_id, ''), coalesce(stripe_subscription_item, ''), created_at, deletion_requested_at, last_quota_warning_at, past_due_at, mfa_enrolled_at, mfa_secret_encrypted, mfa_recovery_codes_hash, mfa_required
from accounts where id = any($1::uuid[]);

-- name: AccountByEmail :one
select id, email, plan, status, coalesce(provider_customer_id, ''), created_at
from accounts where email = $1;

-- name: AccountByKeyHash :one
select a.id, a.email, a.plan, a.status, coalesce(a.provider_customer_id, ''), a.created_at
from accounts a
join api_keys k on k.account_id = a.id
where k.key_sha256 = $1;

-- name: UpdateAccountPlan :exec
update accounts set plan = $2 where id = $1;

-- name: UpdateAccountStatus :exec
update accounts set status = $2 where id = $1;

-- name: CreateAPIKey :one
-- scopes is $4 (text[]). The handler is responsible for validating the
-- scope vocabulary; the store does not. See ADR-034 rev2.
insert into api_keys (account_id, key_sha256, label, scopes)
values ($1, $2, $3, $4)
returning id, account_id, key_sha256, coalesce(label, ''), scopes, created_at, coalesce(last_used_at, 'epoch'::timestamptz);

-- name: DeleteAPIKey :exec
delete from api_keys where id = $1 and account_id = $2;

-- name: DeleteAPIKeyReturning :one
-- IAM-1 (ADR-034 rev2): delete a key and return the row in one
-- statement so the handler can emit `key.deleted` audit with the
-- dismissed scopes. list_secrets-shaped variant of DeleteAPIKey.
delete from api_keys where id = $1 and account_id = $2
returning id, account_id, key_sha256, coalesce(label, ''), scopes, created_at, coalesce(last_used_at, 'epoch'::timestamptz);

-- name: ListAPIKeys :many
-- scopes is the auth permission set surfaced to the dashboard and the
-- /v1/keys listing. See ADR-034 rev2.
select id, account_id, key_sha256, coalesce(label, ''), scopes, created_at, coalesce(last_used_at, 'epoch'::timestamptz)
from api_keys where account_id = $1 order by created_at desc;

-- name: APIKeyByHash :one
-- Used by handlers_auth.go so an operator investigating "who signed in
-- as alice?" can identify the key that authenticated. See ADR-034 rev2.
select id, account_id, key_sha256, coalesce(label, ''), scopes, created_at, coalesce(last_used_at, 'epoch'::timestamptz)
from api_keys where key_sha256 = $1;

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
          coalesce(rootfs_path, ''), coalesce(rootfs_key, ''), coalesce(rootfs_bytes, 0),
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
from crons where app_id = $1 order by created_at desc;

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

-- name: ListEventsByWakeID :many
-- issue #517 / PR-C / ADR-064 — wake-timeline read-side query.
-- Filters on the jsonb expression index events_wake_id_idx
-- (migrations/00114_events_wake_id_idx.sql) and orders by at ASC
-- so the customer-facing timeline endpoint surfaces a forward
-- narrative. The $2 lower bound is the `since` RFC 3339 cursor
-- from the endpoint query string; the $3 limit is bounded to
-- 1000 by the handler. Index path: partial index on
-- (data->>'wake_id') WHERE data->>'wake_id' IS NOT NULL means
-- only rows with a wake_id tag (i.e. the 13 wake.* kinds) are
-- indexed — legacy audit rows are not in scope of PR-C, see
-- ADR-064 §"Compatibility".
select id, at, actor, kind, subject, data
from events
where data->>'wake_id' = $1
  and at > $2
order by at asc
limit $3;

-- name: ListAllEventsPaged :many
-- ADR-091 §3.7 / PR #3 — operator-obs backend audit-reading surface.
-- Reads the live events table (NOT audit_log — distinct source of
-- truth per ADR-091 §3.7.4). Optional filters:
--   * $1 actor    — exact match (handler passes "" to skip)
--   * $2 kind_prefix — LIKE 'prefix%' (handler passes "" to skip)
--   * $3 subject  — exact match (handler passes "" to skip)
--   * $4 since    — RFC 3339 timestamptz (handler passes zero time to skip)
--   * $5 limit    — top-N rows (handler default 200, cap 500;
--                   cast to int8 so sqlc emits int64 Params and the
--                   handler's int→int64 widening is safe)
-- Order: at DESC, id DESC — the id tiebreaker keeps the planner on
-- the (kind, at DESC) index added by 00190_admin_obs_index.sql for
-- kind-prefix queries and avoids an unstable sort on the
-- over-read window.
-- Subject is uuid (nullable in the schema); the cast is left to
-- the handler so the handler can pass an empty string for "no
-- subject filter" without a NULL literal.
select id, at, actor, kind, subject, data
from events
where ($1 = '' or actor = $1)
  and ($2 = '' or kind like $2 || '%')
  and ($3 = '' or subject = $3::uuid)
  and ($4 = '0001-01-01 00:00:00+00:00'::timestamptz or at >= $4)
order by at desc, id desc
limit $5::int8;

-- name: ListRecentEventsForAccount :many
-- ADR-091 §3.7 / PR #3 — per-account events drill-down. Backed by
-- the partial index events_actor_account_idx on
-- (actor_account_id) WHERE actor_account_id IS NOT NULL
-- (migrations/00099_orgs_memberships_invitations.sql). Filters:
--   * $1 actor_account_id — uuid (the account the actor belonged to)
--   * $2 since             — RFC 3339 timestamptz (handler passes
--                            zero time to skip; the predicate is
--                            uniform with ListAllEventsPaged)
--   * $3 limit             — top-N rows (handler default 200, cap 500;
--                            cast to int8 so sqlc emits int64 Params
--                            and the handler's int→int64 widening is
--                            safe)
-- Order: at DESC, id DESC — same rationale as ListAllEventsPaged.
-- PR #3 wires the per-account filter on the SSE mirror's
-- per-account projections; the broader ?actor + ?subject filter
-- shape lives on ListAllEventsPaged.
select id, at, actor, kind, subject, data
from events
where actor_account_id = $1
  and ($2 = '0001-01-01 00:00:00+00:00'::timestamptz or at >= $2)
order by at desc, id desc
limit $3::int8;

-- name: AppendUsage :exec
-- Idempotent on (instance_id, minute) for mb_seconds / requests
-- (M7 hardening, PR feat/m7-beta-hardening): a redelivered
-- minute is a no-op for the billing-floor columns so a meterd
-- restart / network blip / two meterd instances cannot inflate
-- billing. cpu_usec, tx_bytes, net_tx_bytes, net_rx_bytes,
-- cold_boot_count, and tail_seconds are ADDITIVE on the same
-- conflict key — the schedd / meterd accumulators can each call
-- AppendUsage many times within the same minute; the columns are
-- the sum of all per-tick deltas.
--   cpu_usec         — issue #279 / PR-B / ADR-039
--   tx_bytes         — ADR-046 (gateway HTTP response body bytes)
--   net_tx_bytes     — ADR-046 (root-side vethHost.rx_bytes delta)
--   net_rx_bytes     — ADR-048 (root-side vethHost.tx_bytes delta; ingress)
--   cold_boot_count  — ADR-048 (WAKE_RESTORE→WAKE_COLD_BOOT transitions)
--   tail_seconds     — issue #667 / ADR-078 (per-minute wall-clock seconds
--                      draining waitUntil tasks; INFORMATIONAL ONLY — pinned
--                      by pkg/meter/pusher_shadow_test.go::TestPushHour_ExcludesTailSeconds)
insert into usage_minutes (account_id, app_id, instance_id, minute, mb_seconds, requests, cpu_usec, tx_bytes, net_tx_bytes, net_rx_bytes, cold_boot_count, tail_seconds)
values ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12)
on conflict (instance_id, minute) do update
   set cpu_usec        = usage_minutes.cpu_usec        + EXCLUDED.cpu_usec,
       tx_bytes        = usage_minutes.tx_bytes        + EXCLUDED.tx_bytes,
       net_tx_bytes    = usage_minutes.net_tx_bytes    + EXCLUDED.net_tx_bytes,
       net_rx_bytes    = usage_minutes.net_rx_bytes    + EXCLUDED.net_rx_bytes,
       cold_boot_count = usage_minutes.cold_boot_count + EXCLUDED.cold_boot_count,
       tail_seconds    = usage_minutes.tail_seconds    + EXCLUDED.tail_seconds;

-- name: UsageByMonth :many
select account_id, app_id, month, mb_seconds, cpu_usec, requests, tx_bytes, net_tx_bytes
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

-- name: BumpInstanceTailCount :one
-- issue #667 / ADR-078 — atomically apply delta to the instance's
-- `tail_count` column and return the post-update value. The
-- GREATEST(…, 0) floor mirrors DecrementInstanceTailCount's safety
-- property: a stale receipt from a guest that just parked cannot
-- underflow the counter, and the 5s watchdog in snapshotAndPark
-- force-parks regardless. RETURNING tail_count lets the caller
-- (vmmd's MarkInstanceTailTerminal) learn the new value without a
-- follow-up SELECT. Returns ErrNotFound when the instance row is
-- missing (pgx.ErrNoRows maps to state.ErrNotFound in pgstore).
update instances
   set tail_count = GREATEST(tail_count + $2, 0)
 where id = $1
returning tail_count;

-- name: DecrementInstanceTailCount :exec
-- issue #667 / ADR-078 — canonical "tail task reached terminal" path.
-- Equivalent to BumpInstanceTailCount(ctx, id, -n) but kept as a
-- separate method because every decrement site is a terminal event
-- receipt, and the explicit name makes the call sites self-
-- documenting. n is the number of tail tasks to decrement by (1 for
-- the steady-state path, the full unfinished-tail count for the
-- snapshotAndPark watchdog). The GREATEST(…, 0) floor is a
-- defence-in-depth guard against races where a receipt lands after
-- the runner exited cleanly: the counter floors at 0 rather than
-- underflowing, which would permanently stall the schedd reaper's
-- tail_count > 0 early-out. Returns ErrNotFound when the instance
-- row is missing.
update instances
   set tail_count = GREATEST(tail_count - $2, 0)
 where id = $1;

-- name: GetInstanceTailCount :one
-- issue #667 / ADR-078 — read-only probe for the snapshotAndPark
-- 5s watchdog's poll loop. Single SELECT … FROM instances WHERE
-- id = $1; the column is on the hot path so the row is already in
-- shared_buffers under normal load. Returns ErrNotFound when the
-- instance row is missing.
select tail_count from instances where id = $1;

-- name: CreateBuild :one
insert into builds (id, deployment_id, kind, source_bytes, status, log_path)
values (gen_random_uuid(), $1, $2, $3, 'queued', $4)
returning id, deployment_id, kind, source_bytes, status, failure_class, log_path, started_at, finished_at, enqueued_at;

-- name: BuildByID :one
select id, deployment_id, kind, source_bytes, status, failure_class, log_path, started_at, finished_at, enqueued_at
from builds where id = $1;

-- name: BuildByDeployment :one
select id, deployment_id, kind, source_bytes, status, failure_class, log_path, started_at, finished_at, enqueued_at
from builds where deployment_id = $1 order by started_at desc nulls last limit 1;

-- name: UpdateBuildStatus :exec
update builds set
  status = $2,
  failure_class = $3,
  started_at = case when $4::boolean then now() else started_at end,
  finished_at = case when $5::boolean then now() else finished_at end
where id = $1;

-- name: CreateSession :one
-- IAM-3 (ADR-039, issue #187 + #244 merged). One row per dashboard login.
-- Caller has already generated the uuid (the envelope seal needs the same
-- value). issued_ip is an inet ('' cast to NULL means "RemoteAddr
-- unparseable" — surfaced as "" on read by coalesce(host(...))).
insert into sessions (id, account_id, issued_ip, issued_ua)
values ($1, $2, nullif($3, '')::inet, nullif($4, ''))
returning id, account_id,
          coalesce(host(issued_ip), '') as issued_ip,
          coalesce(issued_ua, '') as issued_ua,
          issued_at, last_seen_at, revoked_at;

-- name: GetSession :one
-- Primary-key lookup; called on every authenticated dashboard request.
-- sql.ErrNoRows from pgx maps to state.ErrNotFound in pgstore.
select id, account_id,
       coalesce(host(issued_ip), '') as issued_ip,
       coalesce(issued_ua, '') as issued_ua,
       issued_at, last_seen_at, revoked_at
from sessions where id = $1;

-- name: RevokeSession :one
-- Account-scoped atomic stamp. WHERE includes account_id so a
-- cross-account DELETE returns 0 rows (handler maps false → 404) —
-- IDOR is a persistence invariant, not a handler check.
-- coalesce(revoked_at, now()) makes the call idempotent on already
-- revoked rows (returns 0 rows).
update sessions set revoked_at = coalesce(revoked_at, now())
where id = $1 and account_id = $2 and revoked_at is null
returning id;

-- name: ListSessions :many
-- Active rows only, newest first. Partial index keeps the scan tight.
select id, account_id,
       coalesce(host(issued_ip), '') as issued_ip,
       coalesce(issued_ua, '') as issued_ua,
       issued_at, last_seen_at, revoked_at
from sessions where account_id = $1 and revoked_at is null
order by issued_at desc;

-- name: RevokeAllSessions :many
-- Revokes every active row for accountID except the supplied sid
-- (the calling session). Returns the revoked ids for audit.
update sessions set revoked_at = now()
where account_id = $1 and id <> $2 and revoked_at is null
returning id;

-- name: TouchSessionLastSeen :exec
-- Best-effort, fire-and-forget. Allowed on revoked rows (observability
-- signal only; not authorization). pgx interface returns nothing.
update sessions set last_seen_at = now() where id = $1;

-- name: InsertComputeNodeHeartbeat :exec
-- CP-1 (operator observability): append one row to the heartbeat
-- history. The schedd Heartbeat.Tick goroutine is the only writer.
-- We deliberately do NOT use ON CONFLICT DO NOTHING — a duplicate
-- (node_id, received_at) is observable as a SQLSTATE 23505 unique-
-- violation, which the writer logs as a warning. A silently-deduped
-- stamp would mask a future bug where the scheduler tick fires twice.
-- received_at and last_heartbeat_at are passed by the caller; the
-- column default now() is intentionally NOT used so the writer
-- controls the wall-clock pair (the property test depends on
-- caller-supplied timestamps for deterministic gap classification).
insert into compute_node_heartbeats (node_id, received_at, last_heartbeat_at, source)
values ($1, $2, $3, $4);

-- name: ListComputeNodeHeartbeats :many
-- CP-1: read heartbeat history for one node, newest first. The
-- $2 parameter is nullable: passing pgtype.Timestamptz{} (the Go
-- zero value, mapped to SQL NULL by sqlc) means "no lower bound,
-- return most-recent N"; passing a populated timestamptz means
-- "history since t". The composite index
-- compute_node_heartbeats_node_at_idx (node_id, received_at desc)
-- matches this read shape.
--
-- The endpoint passes a hard-cap limit (default 200, max 2000). The
-- composite index is enough for the routine 30s × 60 nodes × 24h
-- steady-state workload; a 7-day retention sweep is a follow-on.
select id, node_id, received_at, last_heartbeat_at, source
from compute_node_heartbeats
where node_id = $1
  and ($2::timestamptz is null or received_at >= $2)
order by received_at desc
limit $3;

-- --- Organizations (ADR-061, IAM-6, PR 2) -------------------------------
--
-- PR 2's sqlc queries cover the deterministic reads + simple writes. The
-- tx-heavy methods (CreateOrg with initial owner membership; RemoveOrgMember
-- with last-owner FOR UPDATE; ConsumeOrgInvitation with cap check + email
-- equality + membership insert + invitation UPDATE in one tx) stay as
-- inline SQL in pgstore.go — they don't fit the :one / :many / :exec
-- sqlc surface cleanly and the existing precedent (CreateAppIfUnderQuota,
-- ConsumeRecoveryCode, ApplyProjectPlan) renders those inline.

-- name: CreateOrg :one
insert into orgs (
    slug,
    name,
    personal_org,
    personal_owner_account_id,
    plan,
    status,
    provider_customer_id,
    stripe_subscription_item,
    deleted_pending
) values (
    $1, $2, $3, $4, $5, $6, nullif($7, ''), nullif($8, ''), false
)
returning
    id, slug, name, personal_org,
    coalesce(personal_owner_account_id::text, ''),
    plan, status,
    coalesce(provider_customer_id, ''),
    coalesce(stripe_subscription_item, ''),
    deleted_pending,
    created_at, updated_at;

-- name: OrgByID :one
select
    id, slug, name, personal_org,
    coalesce(personal_owner_account_id::text, ''),
    plan, status,
    coalesce(provider_customer_id, ''),
    coalesce(stripe_subscription_item, ''),
    deleted_pending,
    created_at, updated_at
from orgs
where id = $1;

-- name: OrgBySlug :one
select
    id, slug, name, personal_org,
    coalesce(personal_owner_account_id::text, ''),
    plan, status,
    coalesce(provider_customer_id, ''),
    coalesce(stripe_subscription_item, ''),
    deleted_pending,
    created_at, updated_at
from orgs
where lower(slug) = lower($1);

-- name: OrgByPersonalAccount :one
select
    id, slug, name, personal_org,
    coalesce(personal_owner_account_id::text, ''),
    plan, status,
    coalesce(provider_customer_id, ''),
    coalesce(stripe_subscription_item, ''),
    deleted_pending,
    created_at, updated_at
from orgs
where personal_org = true
  and personal_owner_account_id = $1;

-- name: ListOrgsForAccount :many
select
    o.id, o.slug, o.name, o.personal_org,
    coalesce(o.personal_owner_account_id::text, ''),
    o.plan, o.status,
    coalesce(o.provider_customer_id, ''),
    coalesce(o.stripe_subscription_item, ''),
    o.deleted_pending,
    o.created_at, o.updated_at
from orgs o
join org_memberships m on m.org_id = o.id
where m.account_id = $1
  and m.removed_at is null
order by o.slug;

-- name: UpdateOrgPlan :exec
update orgs set plan = $2, updated_at = now() where id = $1;

-- name: UpdateOrgStatus :exec
update orgs set status = $2, updated_at = now() where id = $1;

-- name: SoftDeleteOrg :exec
update orgs set deleted_pending = true, status = 'deleted_pending', updated_at = now() where id = $1;

-- name: ListOrgMembers :many
select
    org_id, account_id, role,
    coalesce(invited_by_account_id::text, ''),
    joined_at, removed_at
from org_memberships
where org_id = $1
order by joined_at;

-- name: OrgMemberByAccount :one
select
    org_id, account_id, role,
    coalesce(invited_by_account_id::text, ''),
    joined_at, removed_at
from org_memberships
where org_id = $1 and account_id = $2;

-- name: OrgInvitationByTokenHash :one
select
    id,
    org_id,
    email::text as email,
    role,
    token_hash,
    coalesce(invited_by_account_id::text, ''),
    expires_at,
    consumed_at,
    revoked_at,
    coalesce(accepting_account_id::text, ''),
    created_at
from org_invitations
where token_hash = $1;

-- name: ListOrgInvitationsForOrg :many
select
    id,
    org_id,
    email::text as email,
    role,
    token_hash,
    coalesce(invited_by_account_id::text, ''),
    expires_at,
    consumed_at,
    revoked_at,
    coalesce(accepting_account_id::text, ''),
    created_at
from org_invitations
where org_id = $1
order by created_at desc;

-- name: ExpireOrgInvitations :execrows
update org_invitations
set revoked_at = now()
where consumed_at is null
  and revoked_at is null
  and expires_at <= $1;

-- name: TrafficAnomalyAggregate :many
-- ADR-091 §3.6 — operator observability backend (PR #2).
-- Hour-of-day baseline over a rolling 7-day window:
--   * baseline is per (account_id, app_id, EXTRACT(HOUR FROM minute))
--   * an anomaly is a row whose current mb_seconds exceeds
--     baseline_mean + 3.0*baseline_stddev (or baseline_mean * 5.0 when
--     baseline_stddev < 1.0 — guards against noisy-low-traffic apps
--     where a tiny stddev explodes the Z-score).
--   * $1 since       — RFC 3339 lower bound for "current" rows
--                      (handler default: now() - 24h, hard cap 168h)
--   * $2 baseline    — RFC 3339 lower bound for the baseline pool
--                      (handler default: now() - 7d, fixed by ADR)
--   * $3 limit       — top-N by deviation (handler default 50, cap 200;
--                      cast to int8 so sqlc emits int64 Params and the
--                      handler's int→int64 widening is safe)
-- Result columns:
--   * account_id, app_id, minute, current_mb_seconds
--   * baseline_mean, baseline_stddev, baseline_samples
--   * z_score, reason ('hour_of_day' | 'raw_z')
-- Index path: usage_minutes primary key (instance_id, minute) is
-- fine for current-minute scans in a 24h window. The 7-day baseline
-- pool scans the same primary key. For the fleet-wide aggregate a
-- future ADR adds (account_id, app_id, minute) as a covering index;
-- PR #2 does NOT add it (single-box posture; multi-host moves to
-- PromQL per ADR-091 §3.6).
with baseline as (
    select account_id,
           app_id,
           extract(hour from usage_minutes.minute) as hour_of_day,
           avg(mb_seconds)::float8 as mean_mb_seconds,
           coalesce(stddev_pop(mb_seconds), 0)::float8 as stddev_mb_seconds,
           count(*)::int as sample_count
    from usage_minutes
    where usage_minutes.minute >= $2
      and usage_minutes.minute <  $1
      and mb_seconds > 0
    group by account_id, app_id, extract(hour from usage_minutes.minute)
),
current_pool as (
    select account_id,
           app_id,
           minute,
           sum(mb_seconds)::float8 as current_mb_seconds
    from usage_minutes
    where minute >= $1
      and mb_seconds > 0
    group by account_id, app_id, minute
),
scored as (
    select c.account_id,
           c.app_id,
           c.minute,
           c.current_mb_seconds,
           b.mean_mb_seconds,
           b.stddev_mb_seconds,
           b.sample_count,
           case
               when b.sample_count < 3 then null
               when b.stddev_mb_seconds < 1.0 and c.current_mb_seconds >= 5.0 * b.mean_mb_seconds
                   and b.mean_mb_seconds > 0 then (c.current_mb_seconds - b.mean_mb_seconds) / 5.0
               when b.stddev_mb_seconds >= 1.0
                   and c.current_mb_seconds >= b.mean_mb_seconds + 3.0 * b.stddev_mb_seconds then
                   (c.current_mb_seconds - b.mean_mb_seconds) / b.stddev_mb_seconds
               else null
           end as z_score,
           case
               when b.stddev_mb_seconds < 1.0 then 'raw_z'
               else 'hour_of_day'
           end as reason
    from current_pool c
    join baseline b
      on c.account_id = b.account_id
     and c.app_id = b.app_id
     and extract(hour from c.minute) = b.hour_of_day
)
select account_id,
       app_id,
       minute,
       current_mb_seconds,
       mean_mb_seconds,
       stddev_mb_seconds,
       sample_count,
       z_score,
       reason
from scored
where z_score is not null
order by z_score desc
limit $3::int8;

-- name: PerAccountRateLimitAggregate :many
-- ADR-091 §3.5 — operator observability backend (PR #2) durable view.
-- Aggregates `events` rows of kind='auth.rate_limited' over a rolling
-- window, grouped by subject (account_id, NULL for anonymous actors).
--   * $1 since  — RFC 3339 lower bound (handler default: now() - 24h,
--                 hard cap 168h per pkg/api/limits.go::ObsAdminWindowMaxHours)
--   * $2 limit  — top-N by hits (handler default 100, cap 500;
--                 cast to int8 so sqlc emits int64 Params and the
--                 handler's int→int64 widening is safe)
-- Anonymous (subject IS NULL) rows are bucketed under a single
-- account_id = NULL row so the operator UI can render the "anon
-- credential stuffing" signal distinctly from named-account bursts.
-- Index path: events_kind_at_idx (added in migration 00190) covers
-- the kind + at DESC predicate. The subject grouping is in-memory
-- after the index scan.
select coalesce(subject, '00000000-0000-0000-0000-000000000000'::uuid) as account_id,
       count(*)::int as hits,
       max(at) as last_event_at
from events
where kind = 'auth.rate_limited'
  and at >= $1
group by coalesce(subject, '00000000-0000-0000-0000-000000000000'::uuid)
order by hits desc, last_event_at desc
limit $2::int8;

