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
select count(*) from apps where account_id = $1 and status in ('active', 'evicted_cold')
  and not (preview_of_slug is not null and coalesce(preview_pr_number, 0) = 0);

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
insert into deployments (id, app_id, build_id, image_digest, kind, source_path, source_root, source_bytes, handler, log_path, status)
values (gen_random_uuid(), $1, null, $2, $3, $4, $5, $6, $7, $8, 'pending')
returning id, app_id, coalesce(build_id::text, ''), image_digest, kind,
          coalesce(source_path, ''), coalesce(source_root, ''), coalesce(source_bytes, 0),
          coalesce(handler, ''), coalesce(log_path, ''),
          status, coalesce(error, ''), created_at;

-- name: DeploymentByID :one
select id, app_id, coalesce(build_id::text, ''), image_digest, kind,
       coalesce(source_path, ''), coalesce(source_root, ''), coalesce(source_bytes, 0),
       coalesce(handler, ''), coalesce(log_path, ''),
       status, coalesce(error, ''), created_at
from deployments where id = $1;

-- name: LatestDeployment :one
select id, app_id, coalesce(build_id::text, ''), image_digest, kind,
       coalesce(source_path, ''), coalesce(source_root, ''), coalesce(source_bytes, 0),
       coalesce(handler, ''), coalesce(log_path, ''),
       status, coalesce(error, ''), created_at
from deployments where app_id = $1 order by created_at desc limit 1;

-- name: ListDeploymentsForApp :many
select id, app_id, coalesce(build_id::text, ''), image_digest, kind,
       coalesce(source_path, ''), coalesce(source_root, ''), coalesce(source_bytes, 0),
       coalesce(handler, ''), coalesce(log_path, ''),
       status, coalesce(error, ''), created_at
from deployments where app_id = $1 order by created_at desc limit $2 offset $3;

-- name: LatestSupersededDeployment :one
select id, app_id, coalesce(build_id::text, ''), image_digest, kind,
       coalesce(source_path, ''), coalesce(source_root, ''), coalesce(source_bytes, 0),
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
          coalesce(source_path, ''), coalesce(source_root, ''), coalesce(source_bytes, 0),
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


-- name: TrafficAnomalyAggregateByNode :many
-- PR #4 (ADR-092 §3.4 amendment) — per-node variant of
-- TrafficAnomalyAggregate. Joins usage_minutes to instances to
-- recover the hosting node_id, then groups by
-- (account_id, app_id, node_id, EXTRACT(HOUR FROM minute)) for
-- the baseline. The current_pool also groups by node_id so the
-- "today" anomaly is per-node, not per-app-wide.
--
-- Why a separate query and not a sqlc parameter on the existing
-- one: the baseline math is identical, but the GROUP BY keys
-- differ by one column, and trying to thread that through a
-- nullable WHERE filter would either lose the per-node grain
-- (NULL filter collapses the group) or return the wrong rollup
-- (a per-node "current" against an app-wide baseline reports
-- spurious anomalies when the fleet is unevenly loaded). A
-- separate query keeps each path simple and self-contained.
--
-- Index path: same as TrafficAnomalyAggregate — usage_minutes
-- primary key (instance_id, minute) is fine for the 24h current
-- window in single-box posture. The instances.node_id lookup
-- is by PK; the join is O(matches) on the PK.
with baseline as (
    select um.account_id,
           um.app_id,
           n.id as node_id,
           extract(hour from um.minute) as hour_of_day,
           avg(um.mb_seconds)::float8 as mean_mb_seconds,
           coalesce(stddev_pop(um.mb_seconds), 0)::float8 as stddev_mb_seconds,
           count(*)::int as sample_count
    from usage_minutes um
    join instances i on i.id = um.instance_id
    join compute_nodes n on n.id = i.node_id
    where um.minute >= $2
      and um.minute <  $1
      and um.mb_seconds > 0
    group by um.account_id, um.app_id, n.id, extract(hour from um.minute)
),
current_pool as (
    select um.account_id,
           um.app_id,
           n.id as node_id,
           um.minute,
           sum(um.mb_seconds)::float8 as current_mb_seconds
    from usage_minutes um
    join instances i on i.id = um.instance_id
    join compute_nodes n on n.id = i.node_id
    where um.minute >= $1
      and um.mb_seconds > 0
    group by um.account_id, um.app_id, n.id, um.minute
),
scored as (
    select c.account_id,
           c.app_id,
           c.node_id,
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
     and c.app_id    = b.app_id
     and c.node_id   = b.node_id
     and extract(hour from c.minute) = b.hour_of_day
)
select account_id,
       app_id,
       node_id,
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

-- ---------------------------------------------------------------------------
-- PR-D / ADR-012 §7 amendment — per-tenant GitHub App webhook secret.
--
-- The two queries below are exposed by pkg/state/pgstore.go as
-- (s *PgStore).UpsertGithubWebhookSecret and
-- (s *PgStore).GetGithubWebhookSecret. The body is hand-curated
-- rather than sqlc-generated because the github_installations pair
-- is also hand-curated (same precedent). The schema lives in
-- migrations/00212_github_webhook_secrets.sql (renumbered from
-- 00208 → 00209 → 00212 in the slot-collision cluster; see the
-- migration's header for the cross-pr-slot-fence chain).
-- ---------------------------------------------------------------------------

-- name: UpsertGithubWebhookSecret :execrows
-- Installs or rotates the per-tenant webhook secret for an
-- installation_id. ON CONFLICT (installation_id) DO UPDATE so a
-- rotation is one statement. upgradedAt + upgradedBy form a §11
-- audit trail.
INSERT INTO github_webhook_secrets (installation_id, secret_value, upgraded_by)
VALUES ($1, $2, $3)
ON CONFLICT (installation_id) DO UPDATE
SET secret_value = EXCLUDED.secret_value,
    upgraded_at  = now(),
    upgraded_by  = EXCLUDED.upgraded_by;

-- name: GetGithubWebhookSecret :one
-- Returns the bytea secret for the given installation_id. The
-- daemon-side resolver treats pgx.ErrNoRows as fail-closed (the
-- webhook is rejected rather than falling back to the platform-
-- wide FAAS_GITHUB_WEBHOOK_SECRET).
SELECT secret_value FROM github_webhook_secrets WHERE installation_id = $1;

-- ---------------------------------------------------------------------------
-- ADR-096 customer-facing automatic error grouping.
-- Tables live in migrations/00222_app_errors.sql. gatewayd-internal
-- writes via the apid gRPC IncrementAppError handler (pkg/apidgrpc/
-- apperrors.proto); apid is the only direct writer to the table
-- per the owner rules. The apid handlers in
-- cmd/apid/handlers_app_errors.go (PR-B) and the nightly purge
-- cron in cmd/apid/app_errors_purge.go (PR-A) read here.
--
-- Index paths pinned in the migration file (NOT regenerated here
-- — sqlc doesn't manage indexes, only the typed query surface).
-- ---------------------------------------------------------------------------

-- name: IncrementAppError :one
-- ADR-096 §3.5 dedupe-merge INSERT. The grpc_server_apperrors.go
-- handler runs this inside a single pgx transaction per stream
-- batch. ON CONFLICT target is app_errors_dedupe_uniq (the
-- migration's UNIQUE on (account_id, app_id, fingerprint)).
-- The dedupe window is enforced by the writer's LRU; this
-- unique constraint is the last-resort tripwire.
--
-- Returns (inserted bool) via the canonical Postgres
-- (xmax = 0) trick: xmax is 0 on a fresh INSERT and non-zero
-- on an UPDATE. This lets the handler distinguish
-- outcomeInserted vs outcomeMerged on the wire — the gateway
-- uses that signal to update its in-process LRU freshness.
INSERT INTO app_errors (
    id, account_id, app_id, deployment_id, fingerprint,
    route, http_status, error_class, sample_message,
    count, request_count, first_seen_at, last_seen_at
) VALUES (
    $1, $2, $3, $4, $5,
    $6, $7, $8, $9,
    1, 1, $10, $10
)
ON CONFLICT (account_id, app_id, fingerprint) DO UPDATE SET
    count         = app_errors.count + 1,
    request_count = app_errors.request_count + 1,
    last_seen_at  = greatest(app_errors.last_seen_at, $10)
RETURNING (xmax = 0) AS inserted;

-- name: InsertAppErrorRequest :exec
-- One row per request that hit the grouped fingerprint. No
-- ON CONFLICT — every request gets its own row. request_count
-- on app_errors is bumped on the paired IncrementAppError
-- call; the read path derives the joined total at query time.
INSERT INTO app_error_requests (
    id, account_id, app_id, fingerprint, request_id, received_at,
    route, http_status, error_class, sample_message,
    deployment_id, headers_sample, redactions
) VALUES (
    $1, $2, $3, $4, $5, $6,
    $7, $8, $9, $10,
    $11, $12, $13
);

-- name: ListAppErrorGroups :many
-- ADR-096 §4.3 summary endpoint. Top-N grouped fingerprints for
-- one (account_id, app_id) over a (since, until) window.
-- Cursor pagination via the (count, last_seen_at, fingerprint)
-- compound tuple (distinct from the operator's (created_at, id)
-- cursor). All three columns are part of the ORDER BY so the
-- cursor predicate must include all three — dropping `count`
-- breaks pagination: rows with smaller count but newer
-- last_seen_at are silently dropped across pages (the cursor
-- predicate on (last_seen_at, fingerprint) only considers the
-- inner order, missing the leading count-DESC boundary).
-- Index path: app_errors_account_app_last_seen_idx covers the
-- primary scan; the (count DESC) sort happens post-filter on the
-- bounded set (limit ≤ AppErrorsSummaryMaxLimit = 100).
--
-- sqlc.arg(name) annotations disambiguate the cursor predicate
-- types — without them sqlc infers the timestamps as timestamptz
-- from the leading (count, last_seen_at) references and breaks
-- pagination.
SELECT
    id, fingerprint, error_class, route, http_status,
    count, request_count, first_seen_at, last_seen_at,
    sample_message
FROM app_errors
WHERE account_id = sqlc.arg('account_id')
  AND app_id     = sqlc.arg('app_id')
  AND last_seen_at >= sqlc.arg('since')
  AND last_seen_at <= sqlc.arg('until')
  AND (sqlc.arg('cursor_count')::bigint IS NULL
       OR count < sqlc.arg('cursor_count')
       OR (count = sqlc.arg('cursor_count')
           AND (last_seen_at, fingerprint) < (sqlc.arg('cursor_last_seen'), sqlc.arg('cursor_fingerprint')::text)))
ORDER BY count DESC, last_seen_at DESC, fingerprint ASC
LIMIT sqlc.arg('limit');

-- name: ListAppErrorRequests :many
-- Drill-down rows for one fingerprint. Cursor paginated via
-- (received_at, request_id). Index path:
-- app_error_requests_drill_idx. Does NOT include headers_sample
-- or redactions — those are returned only by GetAppErrorSample.
--
-- sqlc.arg(name) annotations disambiguate the cursor predicate
-- types — without them sqlc infers $5 as timestamptz from the
-- leading (received_at) reference, breaking pagination.
SELECT
    id, request_id, received_at, route, http_status,
    error_class, sample_message, deployment_id
FROM app_error_requests
WHERE account_id  = sqlc.arg('account_id')
  AND app_id      = sqlc.arg('app_id')
  AND fingerprint = sqlc.arg('fingerprint')
  AND (sqlc.arg('cursor_received_at')::timestamptz IS NULL
       OR (received_at, request_id) < (sqlc.arg('cursor_received_at'), sqlc.arg('cursor_request_id')::uuid))
ORDER BY received_at DESC, request_id DESC
LIMIT sqlc.arg('limit');

-- name: GetAppErrorSample :one
-- Single oldest request row for one fingerprint, used by the
-- UI's "what does this look like" preview. Returns
-- headers_sample + redactions for the wire-side "we redacted
-- X / Y / Z" badge.
SELECT
    id, request_id, received_at, route, http_status,
    error_class, sample_message, deployment_id,
    headers_sample, redactions
FROM app_error_requests
WHERE account_id  = $1
  AND app_id      = $2
  AND fingerprint = $3
ORDER BY received_at ASC, request_id ASC
LIMIT 1;

-- name: ListAppErrorFingerprintsForPurge :many
-- Nightly retention purge read path (cmd/apid/app_errors_purge.go).
-- Returns IDs of app_errors rows for an account older than
-- `cutoff`. Capped at 10000 per call so the DELETE loop can
-- iterate without blocking. Sorted by last_seen_at ASC so the
-- oldest rows are deleted first (a future eviction policy
-- could swap to "least recent activity" without touching this
-- query).
SELECT id FROM app_errors
WHERE account_id = $1
  AND last_seen_at < $2
ORDER BY last_seen_at ASC
LIMIT $3;

-- name: DeleteAppErrorsByIDs :exec
DELETE FROM app_errors WHERE id = ANY($1::uuid[]);

-- name: DeleteAppErrorRequestsByIDs :exec
DELETE FROM app_error_requests WHERE id = ANY($1::uuid[]);

-- name: DeleteAppErrorRequestsOlderThan :exec
-- The retention purge also runs on app_error_requests
-- independently — a customer's drill-down can age out without
-- the parent fingerprint row being removed (e.g. Hobby with
-- 100 rows/fingerprint cap evicts oldest request rows first).
DELETE FROM app_error_requests
WHERE account_id = $1
  AND received_at < $2;

-- ---------------------------------------------------------------------------
-- ADR-098 connection-aware execution (§9.A). Tables live in
-- migrations/00226_data_upstreams.sql. apid is the only writer to
-- data_upstreams (env-classifier side, PR-B); meterd is the only writer
-- to data_upstream_probes (probe loop, PR-C). schedd reads
-- data_upstream_probes via ListDataUpstreamProbesByHostRegion on wake
-- (PR-B wires pkg/sched/upstream_affinity.go). The Store interface is
-- extended in pkg/state/store.go; pgstore + memstore stubs added in
-- PR-A so the surface compiles — production reads/writes land in PR-B.
--
-- Index paths pinned in the migration file (NOT regenerated here —
-- sqlc doesn't manage indexes, only the typed query surface):
--   - data_upstreams_app_created_idx
--   - data_upstreams_host_redacted_idx
--   - data_upstreams_dedupe_uniq (UNIQUE)
--   - partitioned data_upstream_probes + default partition
-- ---------------------------------------------------------------------------

-- name: InsertDataUpstream :one
-- Dedupe-merge INSERT for data_upstreams. Mirrors the
-- IncrementAppError ON CONFLICT pattern (queries.sql:906).
-- The handler (PR-B's cmd/apid/extract.go) targets
-- data_upstreams_dedupe_uniq on (app_id, scope,
-- deployment_scope, kind, host, port) per ADR-098 amendment
-- (issue #954 / 00281_data_upstreams_deployment_scope.sql).
-- On conflict: bump last_seen_at; refresh last_rtt_ms /
-- last_probed_at / declared_region / deployment_scope
-- from EXCLUDED so a re-classification re-stamps the
-- deployment overlay on the latest observation. id is
-- caller-supplied (uuidv7) so the row identity is stable
-- across the dedupe-merge.
INSERT INTO data_upstreams (
    id, account_id, app_id, source, scope, deployment_scope, kind, host, port,
    host_redacted_hash, declared_region,
    last_rtt_ms, last_probed_at,
    last_seen_at, created_at
) VALUES (
    $1, $2, $3, $4, $5, $6, $7, $8, $9,
    $10, $11,
    $12, $13,
    now(), now()
)
ON CONFLICT (app_id, scope, deployment_scope, kind, host, port) DO UPDATE SET
    source          = EXCLUDED.source,
    declared_region = EXCLUDED.declared_region,
    last_rtt_ms     = EXCLUDED.last_rtt_ms,
    last_probed_at  = EXCLUDED.last_probed_at,
    deployment_scope = EXCLUDED.deployment_scope,
    last_seen_at    = now()
RETURNING id;

-- name: ListDataUpstreamsByApp :many
-- GET /v1/apps/{slug}/upstreams (PR-B). Cursor pagination
-- via (created_at, id) — distinct from app_errors'
-- (last_seen_at, fingerprint) cursor because
-- data_upstreams is a stable list, not a hot recency
-- list. Index path: data_upstreams_app_created_idx.
--
-- Optional ?deployment_scope= server-side filter lands via
-- `cursor_deployment_scope` (issue #954 / ADR-098 amendment).
-- Empty string means "no filter; return all deployments"
-- — the wide-open default. Setting a non-empty value restricts
-- to one deployment. Mirrors the existing ?scope= discipline.
--
-- sqlc.arg(...)::type casts disambiguate the cursor params
-- — without them sqlc named both fields `CreatedAt` (taken
-- from the SELECT list) and the generated Go wrapper bound a
-- timestamptz to the $3 uuid slot, tripping a type error on
-- every cursor page past the first. See the cross-PR slot-
-- fence sqlc.arg-disambiguates-cursor memory; the same
-- pattern pins ListAppErrorGroups.
SELECT
    id, account_id, app_id, source, scope, deployment_scope, kind, host, port,
    host_redacted_hash, coalesce(declared_region, ''),
    last_rtt_ms, last_probed_at, last_seen_at, created_at
FROM data_upstreams
WHERE app_id = sqlc.arg('app_id')::uuid
  AND (sqlc.arg('cursor_deployment_scope')::text IS NULL OR sqlc.arg('cursor_deployment_scope')::text = ''
       OR deployment_scope = sqlc.arg('cursor_deployment_scope')::text)
  AND (sqlc.arg('cursor_created_at')::timestamptz IS NULL
       OR (created_at, id) < (sqlc.arg('cursor_created_at')::timestamptz,
                              sqlc.arg('cursor_id')::uuid))
ORDER BY created_at DESC, id DESC
LIMIT sqlc.arg('page_limit')::int;

-- name: GetDataUpstreamByID :one
-- Single-row read for the dashboard's "edit upstream"
-- pane (PR-B). Cursor-safe: no pagination; the handler
-- reads the row directly. Projects the new deployment_scope
-- column (issue #954) so the typed DataUpstream.DeploymentScope
-- in pkg/state/types.go round-trips through sqlc.
SELECT
    id, account_id, app_id, source, scope, deployment_scope, kind, host, port,
    host_redacted_hash, coalesce(declared_region, ''),
    last_rtt_ms, last_probed_at, last_seen_at, created_at
FROM data_upstreams
WHERE id = $1;

-- name: DeleteDataUpstreamByID :exec
-- DELETE /v1/apps/{slug}/upstreams/{id} (PR-B). Soft-
-- delete is rejected by ADR-098 (a soft-deleted row
-- would still trigger pg_notify and confuse schedd);
-- the handler is the only path and uses a hard
-- DELETE. The CASCADE on account_id / app_id handles
-- the GDPR path (delete-account cascades through
-- apps → data_upstreams).
DELETE FROM data_upstreams WHERE id = $1;

-- name: InsertDataUpstreamProbe :exec
-- meterd's probe loop writer (PR-C). One row per
-- (host_redacted_hash, region) per 30s sample. The
-- PK is (id, sampled_at) — id is a caller-supplied
-- uuidv7 so dedupe on retry is trivial. Partitioning
-- on sampled_at gives the meterd loop a hot-write
-- path; the partition creator (PR-C) drops old
-- partitions wholesale.
INSERT INTO data_upstream_probes (
    id, host_redacted_hash, region, kind, sampled_at, rtt_ms,
    ok, error_class, probe_node
) VALUES (
    $1, $2, $3, $4, $5, $6,
    $7, $8, $9
);

-- name: ListDataUpstreamProbesByHostRegion :many
-- schedd's wake-side read path (PR-B/C). Returns the
-- N most recent probe samples for one (host, region)
-- pair, time-windowed to the meterd sliding window
-- (default: 30s × 5min). Index path: the
-- partitioned table's PARTITION BY RANGE on
-- sampled_at — partition pruning drops everything
-- outside the window. The (kind) projection lets
-- schedd key its upstream-affinity map on
-- (kind, region) without joining data_upstreams.
SELECT
    id, host_redacted_hash, region, kind, sampled_at, rtt_ms,
    ok, error_class, coalesce(probe_node, '')
FROM data_upstream_probes
WHERE host_redacted_hash = $1
  AND region = $2
  AND sampled_at >= $3
ORDER BY sampled_at DESC
LIMIT $4;

-- name: PruneDataUpstreamProbesOlderThan :exec
-- Retention purge. The meterd cron calls this
-- hourly with `cutoff = now() - interval '30 days'`
-- (matches the §12 prom_retention_days:15 floor +
-- a 2× safety margin). The partition pruning on
-- sampled_at makes this O(affected partitions),
-- not O(table size). The PR-C partition creator
-- DROPs whole partitions for ranges entirely older
-- than cutoff; this query handles the partial-
-- partition tail (rows in the default partition or
-- the current month that are older than cutoff).
DELETE FROM data_upstream_probes WHERE sampled_at < $1;

-- Issue #757 / ADR-0NN — Trigger primitive (event-source mappings).
-- Mirrors the cron `CreateCron` / `UpdateCron` / `DeleteCron` /
-- `CronByID` / `ListCronsForApp` shape so the apid handler can stay
-- symmetric with the existing cron surface. The dispatch tick
-- (pkg/sched/dispatch_triggers.go, commit #14) writes
-- ClaimTriggerRecords / MarkTriggerRecordSucceeded /
-- MarkTriggerRecordRetry / MarkTriggerRecordDeadLetter, which the
-- schedd uses under FOR UPDATE SKIP LOCKED to drain batches
-- concurrently.
--
-- The FOR UPDATE SKIP LOCKED on ClaimTriggerRecords mirrors the
-- precedent set by ADR-099 PR-C's claim_job_tasks query (issue
-- tracker 'job-task pull'): concurrent schedd replicas each claim
-- disjoint row sets with no advisory-lock plumbing.

-- name: CreateTrigger :one
insert into triggers (account_id, app_id, kind, slug, enabled, config,
                       batch_size_max, batch_window_ms, max_attempts,
                       cron_id, source, payload_max_bytes,
                       broker_poison_strategy)
values ($1, $2, $3, $4, $5, $6::jsonb, $7, $8, $9, $10, $11, $12, $13)
returning id, account_id, app_id, kind, slug, enabled, config,
          batch_size_max, batch_window_ms, max_attempts,
          cron_id, source, payload_max_bytes, broker_poison_strategy,
          created_at, updated_at;

-- name: UpdateTrigger :one
-- Review finding MED-1 (PR #993): the inline SQL at
-- pkg/state/pgstore.go::UpdateTrigger is the source of truth
-- (sqlc-generated UpdateTrigger stub is bypassed because sqlc
-- doesn't model nullable UPDATE parameters); the projection
-- shape is preserved by mirroring the same column list that
-- ListEnabledTriggers uses (filter_criteria is part of the
-- Trigger struct since commit 6 of issue #757 mega-PR).
update triggers set
  enabled = coalesce($2, enabled),
  config = coalesce($3::jsonb, config),
  batch_size_max = coalesce($4, batch_size_max),
  batch_window_ms = coalesce($5, batch_window_ms),
  max_attempts = coalesce($6, max_attempts),
  payload_max_bytes = coalesce($7, payload_max_bytes),
  broker_poison_strategy = coalesce($8, broker_poison_strategy),
  filter_criteria = coalesce($9::jsonb, filter_criteria)
where id = $1
returning id, account_id, app_id, kind, slug, enabled, config,
          batch_size_max, batch_window_ms, max_attempts,
          cron_id, source, payload_max_bytes, broker_poison_strategy,
          filter_criteria,
          created_at, updated_at;

-- name: DeleteTrigger :exec
delete from triggers where id = $1 and app_id = $2;

-- name: TriggerByID :one
-- ADR-118 / commit 6 of the issue #757 mega-PR: filter_criteria
-- is projected so pgstore.TriggerByID returns the same shape as
-- ListEnabledTriggers (sqlc generates identical column sets as
-- the same Go struct; projections that omit a column produce a
-- distinct Row type that breaks the existing pgstore return
-- type).
select id, account_id, app_id, kind, slug, enabled, config,
       batch_size_max, batch_window_ms, max_attempts,
       cron_id, source, payload_max_bytes, broker_poison_strategy,
       filter_criteria,
       created_at, updated_at
from triggers where id = $1;

-- name: ListTriggersForApp :many
-- Same rationale as TriggerByID — full Trigger projection so
-- sqlc's generated Row type matches the existing pgstore return
-- type. (commit 6 of the issue #757 mega-PR.)
select id, account_id, app_id, kind, slug, enabled, config,
       batch_size_max, batch_window_ms, max_attempts,
       cron_id, source, payload_max_bytes, broker_poison_strategy,
       filter_criteria,
       created_at, updated_at
from triggers where app_id = $1 order by created_at desc;

-- name: ListEnabledTriggers :many
-- Pulled by schedd's runTriggerTick on each 1-second cadence. The
-- query is unfiltered by kind because the dispatch tick reads
-- triggers.enabled = true regardless of kind and dispatches via the
-- per-kind poller (pkg/sched/poller.go).
--
-- ADR-118 / issue #757: filter_criteria is included so the dispatch
-- tick can evaluate per-record predicates without a second round-trip
-- (the column is JSONB; empty/null means "no filter").
select id, account_id, app_id, kind, slug, enabled, config,
       batch_size_max, batch_window_ms, max_attempts,
       cron_id, source, payload_max_bytes, broker_poison_strategy,
       filter_criteria,
       created_at, updated_at
from triggers where enabled = true;

-- name: CountTriggersByApp :one
select count(*) from triggers where app_id = $1;

-- name: CountTriggersByAccount :one
select count(*) from triggers t
join apps a on a.id = t.app_id
where a.account_id = $1 and a.status <> 'deleted';

-- name: ClaimTriggerRecords :many
-- FOR UPDATE SKIP LOCKED is the ADR-099 PR-C claim_job_tasks
-- precedent: concurrent schedd replicas each claim disjoint row
-- sets. Returns at most $1 records in (pending, retry) state whose
-- next_fire_at <= now(). The trigger_id constraint scopes the
-- claim so the poller drains one trigger at a time.
select id, trigger_id, item_identifier, payload, headers, metadata,
       state, attempts, next_fire_at, received_at, last_error,
       last_dispatched_at
from trigger_records
where trigger_id = $1
  and state in ('pending','retry')
  and next_fire_at <= now()
order by next_fire_at
limit $2
for update skip locked;

-- name: InsertTriggerRecord :one
-- Review finding #1 (PR #910): the dispatcher MUST persist every
-- broker-delivered record into trigger_records BEFORE
-- ClaimTriggerRecords can find them. Without this insert the
-- entire dispatch tick is dead — ClaimTriggerRecords returns 0
-- rows, the broker messages accumulate forever in poller.inFlight,
-- and the unified Trigger primitive never fires a function.
--
-- ON CONFLICT (trigger_id, item_identifier) DO NOTHING mirrors the
-- broker-side dedupe guarantee (kafka per-partition offset,
-- NATS stream sequence, Redis entry-id, SQS receipt handle,
-- in-platform invocation_id — all globally unique within their
-- own ledger). A re-poll after a partial commit + Ack timeout
-- therefore never inserts a duplicate row.
--
-- Returning id gives the dispatcher the trigger_records.id that
-- ClaimTriggerRecords surfaces under FOR UPDATE SKIP LOCKED,
-- bridging the item_identifier → row_id namespace the
-- ReportBatchItemFailures handler needs.
insert into trigger_records (trigger_id, item_identifier, payload, headers, metadata)
values ($1, $2, $3::jsonb, $4::jsonb, $5::jsonb)
on conflict (trigger_id, item_identifier) do nothing
returning id;

-- name: MarkTriggerRecordSucceeded :exec
update trigger_records
   set state = 'succeeded',
       last_dispatched_at = now()
 where id = $1;

-- name: MarkTriggerRecordRetry :exec
update trigger_records
   set state = 'retry',
       attempts = attempts + 1,
       last_error = $2,
       last_dispatched_at = now(),
       next_fire_at = $3
 where id = $1;

-- name: MarkTriggerRecordDeadLetter :exec
update trigger_records
   set state = 'dead_letter',
       attempts = attempts + 1,
       last_error = $2,
       last_dispatched_at = now()
 where id = $1;

-- name: InsertTriggerDeadLetter :exec
-- One row per dead-lettered record. The reason is the closed-vocab
-- failure mode (rate_limited, poison_record, max_attempts,
-- broker_error, plan_quota, payload_too_large, customer_disabled);
-- the routed_to is the closed-vocab terminal action (drop,
-- manual_retry, customer_dlq). detail carries any per-reason payload
-- (the broker error text, the payload size that tripped the 6MB
-- cap, etc.) for the dashboard read-back.
insert into trigger_dead_letter (record_id, trigger_id, reason, routed_to, detail)
values ($1, $2, $3, $4, $5::jsonb);

-- name: ListTriggerDeadLetter :many
select record_id, trigger_id, reason, routed_to, detail, created_at
from trigger_dead_letter
where trigger_id = $1
order by created_at desc
limit $2;

-- name: ListTriggerRecordsForTrigger :many
-- Used by GET /v1/triggers/{id}/records (dashboard + apid handler).
-- Returns records in dispatch-time order with the standard projection.
select id, trigger_id, item_identifier, payload, headers, metadata,
       state, attempts, next_fire_at, received_at, last_error,
       last_dispatched_at
from trigger_records
where trigger_id = $1
order by received_at desc
limit $2;

-- name: TriggerRecordIDByItemIdentifier :one
-- Audit round 2 finding #1 (PR #910): deadLetterAll() is invoked
-- with broker-side handles (kafka offset, NATS seq, SQS receipt
-- handle, Redis entry-id, queue invocation_id) but the
-- trigger_dead_letter.record_id column is a UUID FK into
-- trigger_records.id. The dispatcher needs to bridge the
-- item_identifier namespace to the row UUID before calling
-- InsertTriggerDeadLetter — otherwise every rate-limit denial
-- trips SQLSTATE 23503 and the dead_letter row is silently
-- dropped, MarkTriggerRecordDeadLetter updates 0 rows, the
-- record stays in poller.inFlight forever, and the broker
-- offset never advances.
--
-- Returns the trigger_records.id for the (trigger_id,
-- item_identifier) pair, or an empty pgtype.UUID (and nil
-- error) when no row exists yet — that case fires when the
-- rate-limit gate denies a record before InsertTriggerRecord
-- has had a chance to run. Callers MUST treat the empty UUID
-- as "skip the dead_letter insert; leave the record in
-- poller.inFlight for the next tick to retry".
select id
from trigger_records
where trigger_id = $1
  and item_identifier = $2;
-- ─── OIDC / keyless deploy auth (ADR-101, issue #270) ───────────────────
--
-- Per-(account_id, issuer_url) trust policy CRUD + exchanged-token
-- CRUD. PR-A scope. The composite PK on oidc_trust_policies is the
-- lookup key; the per-account dashboard list (PR-C) walks the
-- same index. token_hash UNIQUE on oidc_exchanged_tokens is the
-- bearer hot-path lookup.

-- name: GetOIDCTrustPolicy :one
-- Account-by-issuer lookup. Used by the OIDC exchange handler's
-- first-use auto-create path (PR-A) and the dashboard's Refine
-- form (PR-C).
select account_id, issuer_url, jwks_url, audience,
       coalesce(subject_pattern, '') as subject_pattern,
       algorithms, required_claims, created_at, updated_at,
       audit_login
from oidc_trust_policies
where account_id = $1 and issuer_url = $2;

-- name: UpsertOIDCTrustPolicy :one
-- PK conflict on (account_id, issuer_url) updates the mutable
-- columns (audience, subject_pattern, algorithms, required_claims,
-- updated_at, audit_login) and preserves created_at. Returns the
-- full row as stored.
insert into oidc_trust_policies
    (account_id, issuer_url, jwks_url, audience, subject_pattern,
     algorithms, required_claims, audit_login)
values ($1, $2, $3, $4, $5, $6, $7, $8)
on conflict (account_id, issuer_url) do update
    set audience = excluded.audience,
        subject_pattern = excluded.subject_pattern,
        algorithms = excluded.algorithms,
        required_claims = excluded.required_claims,
        updated_at = now(),
        audit_login = excluded.audit_login
returning account_id, issuer_url, jwks_url, audience,
          coalesce(subject_pattern, '') as subject_pattern,
          algorithms, required_claims, created_at, updated_at,
          audit_login;

-- name: ListOIDCTrustPoliciesForAccount :many
-- Per-account dashboard list (PR-C). Empty slice on miss.
select account_id, issuer_url, jwks_url, audience,
       coalesce(subject_pattern, '') as subject_pattern,
       algorithms, required_claims, created_at, updated_at,
       audit_login
from oidc_trust_policies
where account_id = $1
order by created_at desc;

-- name: AccountByOIDCIssuerSubject :one
-- Resolves an OIDC (issuer, subject) pair to the platform
-- account it's bound to. The binding is implicit: any trust
-- policy row with matching issuer_url + subject_pattern that
-- matches the subject claim. Empty subject_pattern = permissive
-- (accept any subject). PR-A matches on issuer_url only with
-- permissive subject semantics; PR-C will refine the per-issuer
-- subject index.
select a.id, a.email, a.plan, a.status,
       coalesce(a.provider_customer_id, ''), a.created_at
from accounts a
join oidc_trust_policies p on p.account_id = a.id
where p.issuer_url = $1
  and (p.subject_pattern is null or p.subject_pattern = ''
       or $2 ~ p.subject_pattern)
order by (p.subject_pattern is null or p.subject_pattern = '') asc,
         length(coalesce(p.subject_pattern, '')) desc,
         a.id
limit 1;

-- name: InsertOIDCExchangedToken :one
-- Fresh-token insert. The id is server-minted by sqlc (gen_random_uuid).
-- Returns the full row (with created_at server-stamped).
insert into oidc_exchanged_tokens
    (account_id, token_hash, expires_at, issuer_url, subject,
     audience, jti)
values ($1, $2, $3, $4, $5, $6, $7)
returning id, account_id, token_hash, expires_at, issuer_url,
          subject, audience, coalesce(jti, '') as jti,
          created_at;

-- name: GetOIDCExchangedTokenByHash :one
-- Bearer hot-path lookup. Filters past-TTL rows out at the SQL
-- layer so the pg contract is "WHERE expires_at > NOW()". The
-- MemStore mirror in pkg/state/memstore.go lazy-deletes instead.
select id, account_id, token_hash, expires_at, issuer_url, subject,
       audience, coalesce(jti, '') as jti, created_at
from oidc_exchanged_tokens
where token_hash = $1
  and expires_at > now();

-- name: DeleteOIDCExchangedToken :exec
-- Operator-driven revoke path (PR-C). Returns 0 rows on miss;
-- the caller maps that to ErrNotFound. The 5-min TTL is the
-- natural expiry path; Delete is the "kill this CI job's
-- credential now" lever.
delete from oidc_exchanged_tokens where id = $1;

-- ---------------------------------------------------------------------------
-- ADR-127 / issue #477 — production debugger per-request telemetry
--
-- The data plane that backs the example insight ("POST /checkout became
-- 38% slower after deployment v81; PostgreSQL queries 82ms → 191ms;
-- 31% of requests affected"). Every gateway-served request lands one row
-- here, keyed on (account_id, app_id, received_at DESC) for the canonical
-- read pattern. trace_id links the row to the in-process TraceRing and
-- to customer-emitted OTel spans.
--
-- Schema is in migrations/00427_request_telemetry.sql (partitioned by
-- RANGE(received_at) so the per-plan retention sweep drops whole monthly
-- partitions rather than per-row DELETEs). Indexes are pinned in the
-- migration; sqlc only generates the typed query surface here.
--
-- Wire path: gatewayd-internal recorder (pkg/gateway/request_telemetry.go)
-- enqueues in-process → publisher batches over unix-socket gRPC to apid's
-- IncrementRequestTelemetry streaming RPC (cmd/apid/grpc_server_request_telemetry.go)
-- → sqlc-generated InsertRequestTelemetry below. The recorder never opens
-- a Postgres connection (CLAUDE.md ownership: apid is the sole writer).
-- ---------------------------------------------------------------------------

-- name: InsertRequestTelemetry :exec
-- One row per gateway-served request. No ON CONFLICT — every request
-- gets its own row (request_id is the natural dedupe, but we don't have
-- it here; TraceRing dedupe is at minute granularity in the recorder).
-- The migration's PK is (id, received_at) because of PARTITION BY RANGE;
-- the id alone is generated by gen_random_uuid() default.
--
-- PR-B (ADR-127 §PR-B): the publisher collapse in
-- pkg/gateway/request_telemetry_publisher.go coalesces requests with
-- the same (app, deployment, route, method, status, minute_bucket) into
-- one row with `count` = the number of originals. count is INT NOT NULL
-- DEFAULT 1 (00440) so pre-PR-B clients keep working — the DEFAULT
-- fires for any INSERT that omits the column. PR-B's publisher always
-- passes it explicitly.
INSERT INTO request_telemetry (
    account_id, app_id, deployment_id, route, method,
    status, latency_ms, cold_boot, trace_id, received_at, count
) VALUES (
    $1, $2, $3, $4, $5,
    $6, $7, $8, $9, $10, $11
);

-- name: ListRequestTelemetryByApp :many
-- Canonical read pattern: "give me the last N requests for this app".
-- Backs GET /v1/apps/{slug}/debug/requests. Uses
-- request_telemetry_app_received_idx. The (since, until) pair is
-- timestamptz; handler-side date parsing is at cmd/apid/
-- handlers_debug_telemetry.go (parseDebugTelemetryWindow).
SELECT id, deployment_id, route, method, status, latency_ms,
       cold_boot, trace_id, received_at
FROM request_telemetry
WHERE app_id = $1
  AND received_at >= $2
  AND received_at <  $3
ORDER BY received_at DESC
LIMIT $4;

-- name: GetRequestTelemetryByAppAndID :one
-- Direct request drill-down for the customer debugger. The app_id
-- predicate is the database-side tenant boundary; the handler has
-- already resolved the slug through the caller's account.
SELECT id, deployment_id, route, method, status, latency_ms,
       cold_boot, trace_id, received_at
FROM request_telemetry
WHERE app_id = $1
  AND id = $2
  AND received_at >= $3
  AND received_at <  $4
LIMIT 1;

-- name: RequestTelemetryByDeployment :many
-- Per-deployment drilldown. Used by gregale debug compare and the
-- regression detector (PR-B). Uses
-- request_telemetry_app_dep_received_idx.
SELECT id, route, method, status, latency_ms, cold_boot, trace_id, received_at
FROM request_telemetry
WHERE app_id = $1
  AND deployment_id = $2
  AND received_at >= $3
  AND received_at <  $4
ORDER BY received_at DESC
LIMIT $5;

-- name: RequestTelemetryBaselineP95ByRoute :many
-- Per-route p50/p95/p99 latency + row count for the
-- compare endpoint and the regression detector (ADR-127 PR-B
-- cron + PR Debugger UX v1 compare handler). Single index scan
-- over the existing request_telemetry_app_dep_received_idx
-- (PR-A migration 00427) so the four aggregates share one
-- window. percentile_cont is the canonical Postgres window-
-- function call; COUNT(*) gives the consistent row count
-- over the same scan so p50/p95/p99 and n can never disagree
-- about which rows contributed.
SELECT route,
       percentile_cont(0.50) WITHIN GROUP (ORDER BY latency_ms)::int AS p50_ms,
       percentile_cont(0.95) WITHIN GROUP (ORDER BY latency_ms)::int AS p95_ms,
       percentile_cont(0.99) WITHIN GROUP (ORDER BY latency_ms)::int AS p99_ms,
       COUNT(*)::bigint                                              AS n
FROM request_telemetry
WHERE app_id = $1
  AND deployment_id = $2
  AND received_at >= $3
  AND received_at <  $4
GROUP BY route;

-- name: RequestTelemetryAnalyticsSummary :one
-- Customer-facing request analytics over a bounded retention window.
-- The recorder collapses identical requests into rows with `count`, so
-- all request/error/cold-boot totals and percentiles must expand that
-- weight rather than treating each stored row as one request.
WITH filtered AS (
    SELECT latency_ms, cold_boot, status, count::bigint AS request_count
    FROM request_telemetry
    WHERE app_id = $1
      AND account_id = $2
      AND received_at >= $3
      AND received_at <  $4
), latency_values AS (
    SELECT latency_ms,
           SUM(request_count)::bigint AS sample_count
    FROM filtered
    GROUP BY latency_ms
), ranked AS (
    SELECT latency_ms,
           sample_count,
           SUM(sample_count) OVER (ORDER BY latency_ms ROWS UNBOUNDED PRECEDING) AS cumulative,
           SUM(sample_count) OVER () AS total
    FROM latency_values
), totals AS (
    SELECT COALESCE(SUM(request_count), 0)::bigint AS requests,
           COALESCE(SUM(request_count) FILTER (WHERE status >= 400), 0)::bigint AS error_requests,
           COALESCE(SUM(request_count) FILTER (WHERE cold_boot), 0)::bigint AS cold_boots
    FROM filtered
)
SELECT requests,
       error_requests,
       cold_boots,
       COALESCE((SELECT MIN(latency_ms) FROM ranked WHERE cumulative >= total * 0.50), 0)::int AS p50_ms,
       COALESCE((SELECT MIN(latency_ms) FROM ranked WHERE cumulative >= total * 0.95), 0)::int AS p95_ms,
       COALESCE((SELECT MIN(latency_ms) FROM ranked WHERE cumulative >= total * 0.99), 0)::int AS p99_ms
FROM totals;

-- name: RequestTelemetryAnalyticsByRoute :many
-- Top route/method rows for the customer analytics overview. `count` is
-- weighted throughout the same way as RequestTelemetryAnalyticsSummary.
WITH filtered AS (
    SELECT route, method, latency_ms, cold_boot, status,
           count::bigint AS request_count
    FROM request_telemetry
    WHERE app_id = $1
      AND account_id = $2
      AND received_at >= $3
      AND received_at <  $4
), route_totals AS (
    SELECT route,
           method,
           COALESCE(SUM(request_count), 0)::bigint AS requests,
           COALESCE(SUM(request_count) FILTER (WHERE status >= 400), 0)::bigint AS error_requests,
           COALESCE(SUM(request_count) FILTER (WHERE cold_boot), 0)::bigint AS cold_boots
    FROM filtered
    GROUP BY route, method
), latency_values AS (
    SELECT route,
           method,
           latency_ms,
           SUM(request_count)::bigint AS sample_count
    FROM filtered
    GROUP BY route, method, latency_ms
), ranked AS (
    SELECT route,
           method,
           latency_ms,
           sample_count,
           SUM(sample_count) OVER (PARTITION BY route, method ORDER BY latency_ms ROWS UNBOUNDED PRECEDING) AS cumulative,
           SUM(sample_count) OVER (PARTITION BY route, method) AS total
    FROM latency_values
), percentiles AS (
    SELECT route,
           method,
           COALESCE(MIN(latency_ms) FILTER (WHERE cumulative >= total * 0.50), 0)::int AS p50_ms,
           COALESCE(MIN(latency_ms) FILTER (WHERE cumulative >= total * 0.95), 0)::int AS p95_ms,
           COALESCE(MIN(latency_ms) FILTER (WHERE cumulative >= total * 0.99), 0)::int AS p99_ms
    FROM ranked
    GROUP BY route, method
)
SELECT totals.route,
       totals.method,
       totals.requests,
       totals.error_requests,
       totals.cold_boots,
       percentiles.p50_ms,
       percentiles.p95_ms,
       percentiles.p99_ms
FROM route_totals AS totals
JOIN percentiles USING (route, method)
ORDER BY totals.requests DESC, totals.method ASC, totals.route ASC
LIMIT $5;

-- name: RequestTelemetryAnalyticsTimeseries :many
-- Zero-filled UTC hourly request analytics for customer charts. The
-- recorder collapses rows by count, so all totals and percentile ranks
-- expand that weight rather than counting stored rows.
WITH buckets AS (
    SELECT generate_series(
        date_bin('1 hour', sqlc.arg('received_at')::timestamptz, 'epoch'::timestamptz),
        date_bin('1 hour', sqlc.arg('received_at_2')::timestamptz - interval '1 microsecond', 'epoch'::timestamptz),
        interval '1 hour'
    )::timestamptz AS bucket_start
), filtered AS (
    SELECT date_bin('1 hour', received_at, 'epoch'::timestamptz) AS bucket_start,
           latency_ms,
           cold_boot,
           status,
           count::bigint AS request_count
    FROM request_telemetry
    WHERE app_id = $1
      AND account_id = $2
      AND received_at >= sqlc.arg('received_at')::timestamptz
      AND received_at <  sqlc.arg('received_at_2')::timestamptz
      AND (sqlc.arg('route')::text = '' OR route = sqlc.arg('route')::text)
      AND (sqlc.arg('method')::text = '' OR method = sqlc.arg('method')::text)
), latency_values AS (
    SELECT bucket_start,
           latency_ms,
           SUM(request_count)::bigint AS sample_count
    FROM filtered
    GROUP BY bucket_start, latency_ms
), ranked AS (
    SELECT bucket_start,
           latency_ms,
           sample_count,
           SUM(sample_count) OVER (PARTITION BY bucket_start ORDER BY latency_ms ROWS UNBOUNDED PRECEDING) AS cumulative,
           SUM(sample_count) OVER (PARTITION BY bucket_start) AS total
    FROM latency_values
), percentiles AS (
    SELECT bucket_start,
           COALESCE(MIN(latency_ms) FILTER (WHERE cumulative >= total * 0.50), 0)::int AS p50_ms,
           COALESCE(MIN(latency_ms) FILTER (WHERE cumulative >= total * 0.95), 0)::int AS p95_ms,
           COALESCE(MIN(latency_ms) FILTER (WHERE cumulative >= total * 0.99), 0)::int AS p99_ms
    FROM ranked
    GROUP BY bucket_start
), totals AS (
    SELECT bucket_start,
           COALESCE(SUM(request_count), 0)::bigint AS requests,
           COALESCE(SUM(request_count) FILTER (WHERE status >= 400), 0)::bigint AS error_requests,
           COALESCE(SUM(request_count) FILTER (WHERE cold_boot), 0)::bigint AS cold_boots
    FROM filtered
    GROUP BY bucket_start
)
SELECT b.bucket_start,
       COALESCE(t.requests, 0)::bigint AS requests,
       COALESCE(t.error_requests, 0)::bigint AS error_requests,
       COALESCE(t.cold_boots, 0)::bigint AS cold_boots,
       COALESCE(p.p50_ms, 0)::int AS p50_ms,
       COALESCE(p.p95_ms, 0)::int AS p95_ms,
       COALESCE(p.p99_ms, 0)::int AS p99_ms
FROM buckets AS b
LEFT JOIN totals AS t USING (bucket_start)
LEFT JOIN percentiles AS p USING (bucket_start)
ORDER BY b.bucket_start ASC;

-- PR-B (ADR-127 §PR-B) — regression observation persistence + dashboard
-- read patterns. The cron in cmd/apid/debug_regression_cron.go composes
-- RequestTelemetryBaselineP95ByRoute (above) + RequestTelemetryByDeployment
-- in Go to detect per-route regressions (ADR-127 §Decision 5: p95_ms
-- exceeds p95_base_ms * 1.20). The detections persist here.
--
-- A native sqlc RequestTelemetryRegression query was tried during the PR-A
-- recon: the CTE-on-CTE shape trips sqlc v1.31's "ambiguous column
-- reference" parser on (deployment_id) even after explicit ON clauses;
-- the workaround is a LATERAL join that requires a more invasive query
-- refactor. Go-side composition of the two PR-A queries above is the
-- chosen workaround (cheap, no parser gymnastics).

-- name: UpsertRegressionObservation :exec
-- Persist a regression observation. PRIMARY KEY (app_id, deployment_id,
-- route) — the cron upserts on this triple so the table grows at most
-- one row per (deployment, route) across all cron passes, not one row
-- per cron tick. Mirrors UpsertDoctorObservation's primary-key upsert
-- shape (migrations/00313). first_detected_at is set on INSERT only;
-- the ON CONFLICT clause does NOT touch it, so the column survives
-- subsequent upserts and the dashboard shows "regression detected 4h
-- ago" correctly. last_detected_at is refreshed to EXCLUDED on every
-- pass; the column backs the `since=<duration>` filter on the dashboard
-- and the GET /v1/apps/{slug}/debug/regressions endpoint.
INSERT INTO debug_regression_observations (
    app_id, deployment_id, route,
    p95_ms, p95_base_ms, affected_count,
    regression_factor, last_detected_at
) VALUES (
    $1, $2, $3,
    $4, $5, $6,
    $7, now()
)
ON CONFLICT (app_id, deployment_id, route) DO UPDATE SET
    p95_ms            = EXCLUDED.p95_ms,
    p95_base_ms       = EXCLUDED.p95_base_ms,
    affected_count    = EXCLUDED.affected_count,
    regression_factor = EXCLUDED.regression_factor,
    last_detected_at  = EXCLUDED.last_detected_at;

-- name: ListActiveRegressionsByApp :many
-- Dashboard + GET /v1/apps/{slug}/debug/regressions read pattern.
-- `since` is an interval (e.g. '1 hour') clamped handler-side to the
-- plan's DebugTelemetryRetentionDays cap. ORDER BY regression_factor
-- DESC, last_detected_at DESC matches the dashboard render order
-- (worst regression at the top, then most-recently-reconfirmed).
-- Uses debug_regression_observations_app_idx (00436).
SELECT deployment_id, route,
       p95_ms, p95_base_ms, affected_count,
       regression_factor, first_detected_at, last_detected_at
FROM debug_regression_observations
WHERE app_id = $1
  AND last_detected_at > now() - $2::interval
ORDER BY regression_factor DESC, last_detected_at DESC;

-- name: ListDeploymentsForCompare :many
-- Backs the dashboard compare panel's two `<select>` dropdowns: "pick
-- source deployment" and "pick mirror deployment". GROUP BY on
-- request_telemetry reads the distinct deployment_ids that have
-- shipped traffic in the window. first_seen/last_seen row_count give
-- the dashboard enough metadata to render "v81 — 17m of traffic,
-- 4123 rows" without a second query.
--
-- PR-B uses this to seed the dropdowns; the actual percentile
-- comparison is two RequestTelemetryBaselineP95ByRoute calls in Go
-- (PR-A ships that query; the regression cron uses the same shape).
SELECT deployment_id,
       MIN(received_at) AS first_seen,
       MAX(received_at) AS last_seen,
       COUNT(*)         AS row_count
FROM request_telemetry
WHERE app_id = $1
  AND received_at > now() - $2::interval
GROUP BY deployment_id
ORDER BY last_seen DESC
LIMIT $3;

-- name: ListAppsWithRecentTelemetry :many
-- Backs the regression cron's discovery loop. Walks all apps with
-- >=1 row in the regression window so the cron doesn't have to query
-- the full `apps` table on every tick. Returns the app_id and slug
-- (the cron needs slug for log lines; the rest of the cron needs
-- app_id to bound RequestTelemetryBaselineP95ByRoute / ByDeployment).
-- Index: request_telemetry_app_received_idx on (app_id, received_at
-- DESC) makes this DISTINCT scan cheap.
SELECT DISTINCT app_id
FROM request_telemetry
WHERE received_at > now() - $1::interval;

-- name: UpdateSpansSummary :exec
-- ADR-127 PR-D: writer for spans_summary jsonb. The gatewayd-public
-- OTLP/HTTP handler coalesces incoming batches for the same trace_id
-- in-process (pkg/gateway/spans_accumulator.go) and flushes the
-- accumulated summary every FAAS_OTEL_FLUSH_INTERVAL (default 30s).
-- UPDATE (not INSERT) because the row already exists — the recorder
-- wrote it from the gateway edge
-- (pkg/gateway/request_telemetry_publisher.go). Last-writer-wins on
-- concurrent UPDATEs is acceptable; the 24h window bounds the index
-- seek to the partial index request_telemetry_trace_idx selectivity.
-- $N::jsonb cast is load-bearing — without it sqlc binds as text and
-- Postgres raises SQLSTATE 22P02 (invalid_text_representation).
--
-- PR-D code-review #1: the WHERE clause now also pins account_id.
-- Defense in depth against cross-customer overwrite. The
-- gateway-side accumulator already rejects trace_id/account_id
-- mismatches (pkg/gateway/spans_accumulator.go ErrAccountMismatch),
-- and the apid gRPC handler forwards the same account_id, but a
-- bug at any layer could otherwise let account A's flush wipe
-- account B's row. Adding the account_id = $3::uuid predicate
-- makes cross-customer overwrite impossible regardless of which
-- upstream guard fails. The composite (trace_id, account_id)
-- lookup still hits request_telemetry_trace_idx for the trace_id
-- selectivity; the residual account_id check is a post-fetch
-- row-level filter (one row, microseconds).
update request_telemetry
   set spans_summary = $2::jsonb
 where trace_id = $1
   and account_id = $3::uuid
   and received_at >= now() - interval '24 hours';

-- ---------------------------------------------------------------------------
-- Issue #246 acceptance item 7 — hard-bounce + complaint suppression list
-- (ADR-115 §D.3, RFC 8058 follow-on). One row per (source,
-- provider_event_id) so Resend's webhook redelivery dedupes to the
-- same row instead of double-suppressing. The unique index is the
-- dedupe key. Schema lives in migrations/00562_mail_suppressions.sql.
-- ---------------------------------------------------------------------------

-- name: RecordMailSuppression :one
-- INSERT with ON CONFLICT (source, provider_event_id) DO UPDATE so
-- the Resend webhook redelivery is idempotent. RETURNING (xmax = 0)
-- exposes the canonical "fresh insert vs replay" signal — the
-- bounce handler (pkg/meter/bounce_handler.go) reads it to decide
-- whether to advance dunning (fresh) or skip (replay). The SET
-- clause is intentionally a no-op rewrite of email: the row's
-- contents are already correct, but Postgres needs an UPDATE arm
-- to fire RETURNING when the conflict hits.
--
-- $1 = account_id (nullable — the bounce handler may not have
--      correlated the address to an account yet)
-- $2 = email
-- $3 = reason (closed: hard_bounce / complaint / manual)
-- $4 = source (closed: resend / postmark / operator)
-- $5 = provider_event_id
-- $6 = expires_at (nullable — null means suppression is permanent
--      until operator override; non-null is the TTL deadline)
INSERT INTO mail_suppressions (
    account_id, email, reason, source, provider_event_id, expires_at
) VALUES ($1, $2, $3, $4, $5, $6)
ON CONFLICT (source, provider_event_id) DO UPDATE
SET email = EXCLUDED.email
RETURNING (xmax = 0) AS inserted;

-- name: IsMailSuppressed :one
-- Returns true if any active suppression matches the address.
-- "Active" means expires_at IS NULL OR expires_at > now(); the
-- partial index mail_suppressions_active_email_idx keeps expired
-- rows out so a row that fell out of TTL doesn't block future
-- mail to that address. Lower-casing on both sides makes the
-- match case-insensitive (Postfix accepts mixed case; the
-- providers' bounce webhooks do too).
--
-- $1 = email
SELECT EXISTS (
    SELECT 1 FROM mail_suppressions
    WHERE lower(email) = lower($1)
      AND (expires_at IS NULL OR expires_at > now())
) AS suppressed;

-- ----------------------------------------------------------------------
-- NodeLifecycleStore (Workstream B, issue #1184)
--
-- 12 queries wrap the recovery arbiter's DB I/O. The arbiter reads via
-- NodeGet/NodeList/NodeListRecoverable/NodeListDrainable and writes via
-- NodeSetLifecycle (CAS on the prior lifecycle, so two competing writers
-- can't race from 'active'→'draining' vs 'active'→'unavailable'). Drain
-- initiation stamps `drain_initiated_at`; the drain-complete sweep marks
-- `drain_completed_at` once the last live instance migrates or recreates.
--
-- DeploymentRecordSnapshotMiss / DeploymentClearSnapshotBackoff are the
-- per-deployment backoff state added by 00585 — wake flow records a
-- miss and stamps a `Retry-After` until, the recovery arbiter clears
-- it after a successful migrate-or-recreate sweep. Partial index
-- `deployments_snapshot_backoff_idx` makes the wake-side check an
-- index-only scan.
-- ----------------------------------------------------------------------

-- name: NodeGet :one
-- Resolve a single compute_nodes row by its UUID. Used by the recovery
-- arbiter's per-tick decision and by the apid drain handler.
SELECT
    id, name, target_url, vpcpus, mem_mb, max_concurrency,
    admission_ceiling_mb,
    lifecycle::text AS lifecycle, active, last_heartbeat_at, created_at,
    region, zone, schedd_target_url, vcpu_budget, public_ip, public_ip_set_at,
    release_id, manifest_hash, host_certificate, cert_fingerprint, role,
    generation, gateway_target_url,
    drain_initiated_at, drain_completed_at, recovery_initiated_at,
    last_recovery_outcome
FROM compute_nodes
WHERE id = $1;

-- name: NodeGetByName :one
-- Same as NodeGet but by the human-stable name. The apid handler
-- (POST /v1/compute-nodes/{name}/drain) and the recovery arbiter's
-- cold-start reconciliation path both key by name because operator
-- input is name-based.
SELECT
    id, name, target_url, vpcpus, mem_mb, max_concurrency,
    admission_ceiling_mb,
    lifecycle::text AS lifecycle, active, last_heartbeat_at, created_at,
    region, zone, schedd_target_url, vcpu_budget, public_ip, public_ip_set_at,
    release_id, manifest_hash, host_certificate, cert_fingerprint, role,
    generation, gateway_target_url,
    drain_initiated_at, drain_completed_at, recovery_initiated_at,
    last_recovery_outcome
FROM compute_nodes
WHERE name = $1;

-- name: NodeList :many
-- All nodes, optionally filtered by lifecycle. The recovery arbiter
-- passes NULL to enumerate every row on cold-start reconciliation;
-- the placement filter passes lifecycle='active' (via the existing
-- `WHERE active = true` partial-index path — unchanged). $1 is the
-- lifecycle filter; pass the empty string for "any".
SELECT
    id, name, target_url, vpcpus, mem_mb, max_concurrency,
    admission_ceiling_mb,
    lifecycle::text AS lifecycle, active, last_heartbeat_at, created_at,
    region, zone, schedd_target_url, vcpu_budget, public_ip, public_ip_set_at,
    release_id, manifest_hash, host_certificate, cert_fingerprint, role,
    generation, gateway_target_url,
    drain_initiated_at, drain_completed_at, recovery_initiated_at,
    last_recovery_outcome
FROM compute_nodes
WHERE ($1 = '' OR lifecycle::text = $1)
ORDER BY name;

-- name: NodeSetLifecycle :execrows
-- CAS lifecycle transition. Returns 0 if the prior state didn't match
-- $2 (i.e. another writer raced us) — the caller treats that as a
-- soft no-op and re-reads via NodeGet. Returns 1 on success.
--
-- Args:
--   $1 = node id (UUID)
--   $2 = expected prior lifecycle text
--   $3 = new lifecycle text
--   $4 = wall-clock timestamp to stamp on the relevant audit column:
--        'draining'        → drain_initiated_at
--        'unavailable'     → NULL (heartbeat gap is the writer; this
--                             path is for the rare explicit flip)
--        'recovering'      → recovery_initiated_at
--        'active'          → drain_completed_at (last step of a
--                             successful drain) OR NULL when called
--                             from the heartbeat reactivator
UPDATE compute_nodes
SET lifecycle = $3::compute_node_lifecycle,
    drain_initiated_at    = CASE WHEN $3::compute_node_lifecycle = 'draining'  THEN $4 ELSE drain_initiated_at    END,
    recovery_initiated_at = CASE WHEN $3::compute_node_lifecycle = 'recovering' THEN $4 ELSE recovery_initiated_at END,
    drain_completed_at    = CASE WHEN $3::compute_node_lifecycle = 'active'     THEN $4 ELSE drain_completed_at    END
WHERE id = $1
  AND lifecycle::text = $2;

-- name: NodeMarkDrainCompleted :execrows
-- Stamps drain_completed_at + flips lifecycle='active'. Called once
-- the drain arbiter confirms zero live instances remain on the node.
-- CAS on lifecycle='draining' so a concurrent reactivate can't race.
UPDATE compute_nodes
SET lifecycle         = 'active',
    drain_completed_at = $2
WHERE id = $1
  AND lifecycle = 'draining';

-- name: NodeMarkRecovered :execrows
-- Stamps last_recovery_outcome='succeeded' and flips lifecycle='active'.
-- Called by the recovery arbiter after the migrate-or-recreate sweep
-- has cleared all stranded instances on a 'recovering' node. CAS on
-- 'recovering' so a fresh heartbeat-driven reactivate wins cleanly.
UPDATE compute_nodes
SET lifecycle            = 'active',
    last_recovery_outcome = 'succeeded'
WHERE id = $1
  AND lifecycle = 'recovering';

-- name: NodeListRecoverable :many
-- Nodes that need the recovery arbiter's attention. Two lifecycle
-- states qualify:
--   'unavailable'  → heartbeat gap detected; instances stranded.
--   'recovering'   → first post-failure ping succeeded; sweep to
--                    confirm zero stranded instances.
-- Caller is the recovery arbiter; one tick enumerates both classes
-- and applies the same decision matrix.
SELECT
    id, name, target_url, vpcpus, mem_mb, max_concurrency,
    admission_ceiling_mb,
    lifecycle::text AS lifecycle, active, last_heartbeat_at, created_at,
    region, zone, schedd_target_url, vcpu_budget, public_ip, public_ip_set_at,
    release_id, manifest_hash, host_certificate, cert_fingerprint, role,
    generation, gateway_target_url,
    drain_initiated_at, drain_completed_at, recovery_initiated_at,
    last_recovery_outcome
FROM compute_nodes
WHERE lifecycle IN ('unavailable', 'recovering')
ORDER BY name;

-- name: NodeListDrainable :many
-- Drainable candidates: lifecycle='active' AND zero live instances.
-- The drain handler refuses to flip lifecycle='draining' for nodes
-- with active traffic (it surfaces RFC 7807 `node_draining_refused`
-- instead) and waits for the operator to clear the load first; this
-- query is the "safe to drain right now" enumeration.
SELECT
    id, name, target_url, vpcpus, mem_mb, max_concurrency,
    admission_ceiling_mb,
    lifecycle::text AS lifecycle, active, last_heartbeat_at, created_at,
    region, zone, schedd_target_url, vcpu_budget, public_ip, public_ip_set_at,
    release_id, manifest_hash, host_certificate, cert_fingerprint, role,
    generation, gateway_target_url,
    drain_initiated_at, drain_completed_at, recovery_initiated_at,
    last_recovery_outcome
FROM compute_nodes
WHERE lifecycle = 'active'
  AND NOT EXISTS (
      SELECT 1 FROM instances
      WHERE instances.node_id = compute_nodes.id
        AND instances.state IN ('running', 'cold_booting', 'waking', 'snapshotting', 'migrating')
  )
ORDER BY name;

-- name: InstanceListByNodeForRecovery :many
-- Live instances on a specific node — input to the arbiter's
-- per-instance decision. Limited to states the arbiter can act on:
-- 'running' (live-migrate), 'cold_booting' (recreate, the snapshot
-- may not have made it to the destination yet), 'waking' (recreate —
-- same reason). The arbiter only needs the
-- (app_id, deployment_id, state, id) tuple — account_id is reachable
-- via the existing app/deployment joins if needed by downstream
-- code, but the per-tick hot loop doesn't pay for it here.
SELECT id, state, app_id, deployment_id
FROM instances
WHERE node_id = $1
  AND state IN ('running', 'cold_booting', 'waking', 'snapshotting', 'migrating')
ORDER BY started_at;

-- name: DeploymentRecordSnapshotMiss :exec
-- Bump snapshot_miss_count + stamp Retry-After until. Called by the
-- wake flow when the snapshot-fetch path fails (stale cache, missing
-- replica on the destination, etc.). Capped-exponential backoff math
-- lives in pkg/sched/snapshot_backoff.go; this query is the state
-- write only.
--
-- $1 = deployment id
-- $2 = backoff_until timestamp
UPDATE deployments
SET snapshot_miss_count          = snapshot_miss_count + 1,
    snapshot_miss_last_at        = now(),
    snapshot_miss_backoff_until  = $2
WHERE id = $1;

-- name: DeploymentClearSnapshotBackoff :exec
-- Called by the recovery arbiter after a successful migrate-or-
-- recreate sweep has restored the destination's snapshot set, OR by
-- the wake flow on a successful cold boot. Resets the counter and
-- clears the backoff_until so future wakes don't short-circuit.
UPDATE deployments
SET snapshot_miss_count         = 0,
    snapshot_miss_last_at       = NULL,
    snapshot_miss_backoff_until = NULL
WHERE id = $1;

-- name: DeploymentSnapshotBackoffActive :one
-- The wake-side gate. Returns the row while a backoff timestamp is
-- present; the store computes whether it is still active. Returning
-- expired rows preserves the miss count for the next backoff stamp.
-- The partial index `deployments_snapshot_backoff_idx` covers this lookup.
SELECT snapshot_miss_count, snapshot_miss_backoff_until
FROM deployments
WHERE id = $1
  AND snapshot_miss_backoff_until IS NOT NULL;

-- =====================================================================

-- name: CreateUploadSession :one
-- Inserts a fresh upload_sessions row. The handler pre-validates
-- total_size against limits.SourceTarballMaxMB (pkg/api/limits.go)
-- and the per-account open-session cap (5 per (account_id, app_slug))
-- before this INSERT — sqlc only owns the type-safe binding. The
-- 1-GiB hard ceiling in the SQL CHECK is the worst-case spool size,
-- not the customer-facing quota.
INSERT INTO upload_sessions (
    id, account_id, app_slug, total_size, chunk_size, sha256_hex, part_path, deploy_options
) VALUES (
    $1, $2, $3, $4, $5, $6, $7, $8
)
RETURNING id, account_id, app_slug, total_size, received_bytes, chunk_size,
          sha256_hex, part_path, status, created_at, last_patched_at, expires_at,
          deployment_id, deploy_options;

-- name: GetUploadSession :one
-- Reads a single upload_sessions row by id. Used by:
--   (a) the handler's POST /commit pre-check (validate status='open',
--       received_bytes == total_size before validating tar shape);
--   (b) the CLI's GET-when-resuming-after-network-drop path
--       (PR-2 cmd/gregale/upload_session.go) which learns the
--       server's current received_bytes to compute the next chunk's
--       Upload-Offset header.
-- No FOR UPDATE here — the row is append-only under normal operation
-- and the CAS in AppendUploadBytes is the serialisation point. If
-- future work needs a transactional read-modify-write (e.g., admin
-- force-close), add a separate GetUploadSessionForUpdate :one.
SELECT id, account_id, app_slug, total_size, received_bytes, chunk_size,
       sha256_hex, part_path, status, created_at, last_patched_at, expires_at,
       deployment_id, deploy_options
FROM upload_sessions
WHERE id = $1;

-- name: AppendUploadBytes :one
-- The atomic CAS that makes the resumable protocol safe under
-- concurrent PATCHes on the same upload_id. The handler reads
-- the client's Upload-Offset header (the offset the client claims
-- the server is currently at) and the chunk_size it sent, then
-- computes expected_new = client_offset + chunk_bytes. The WHERE
-- clause pins the row to the upload id and the explicitly named expected
-- offset — a row whose received_bytes has already
-- advanced (e.g., a racing PATCH from a retry) returns 0 rows and
-- the handler maps that to 409 Conflict with the actual current
-- offset in the body.
--
-- RETURNING exposes the new received_bytes so the handler doesn't
-- need a follow-up SELECT on the happy path. last_patched_at is
-- bumped to now() so the reaper's idle-aware expiry (NOT in PR-1;
-- deferred — see plan "Out of scope") has a fresh anchor.
UPDATE upload_sessions
   SET received_bytes = sqlc.arg(new_received_bytes),
       last_patched_at = now()
 WHERE id = sqlc.arg(id)
   AND status = 'open'
   AND expires_at > now()
   AND received_bytes = sqlc.arg(expected_received_bytes)
RETURNING received_bytes, total_size;

-- name: MarkUploadSessionCommitted :one
-- Final state transition: open → committed. The handler runs
-- validateTarballShape + scanForStatefulShape (cmd/apid/
-- deploy_inputs.go:291-447) BEFORE this UPDATE so a commit that
-- fails validation leaves the row at status='open' and the .part
-- file in place for retry. deployment_id is set after the build
-- row is enqueued (apidsource.Enqueue) so the row points at the
-- deployment that consumed the .part.
--
-- The UPDATE WHERE status='open' is the second-line idempotency
-- guard: a retry of POST /v1/uploads/{id}/commit that races with
-- itself hits 0 rows and the handler reads upload_commit_outcomes
-- to return the original deployment_id.
UPDATE upload_sessions
   SET status = 'committed',
       deployment_id = $2
 WHERE id = $1
   AND status = 'open'
RETURNING id, status, deployment_id;

-- name: CancelUploadSession :exec
-- Explicit cancel from DELETE /v1/uploads/{id}. The handler also
-- removes the .part file via os.Remove AFTER the UPDATE commits —
-- doing it before would leak a file if the UPDATE rolled back.
-- Status transition is open → cancelled; a second cancel or a
-- commit-after-cancel hits 0 rows and the handler returns 409
-- upload_session_already_cancelled.
UPDATE upload_sessions
   SET status = 'cancelled'
 WHERE id = $1
   AND account_id = $2::uuid
   AND status = 'open';

-- name: ReapExpiredUploadSessions :many
-- The reaper's scan query (cmd/apid/upload_session_reaper.go).
-- Returns at most 100 rows per invocation to bound memory; the
-- goroutine ticker at cmd/apid/main.go re-invokes on its 5-minute
-- cadence. partial index upload_sessions_expires_idx makes this
-- an index-only scan over the open sessions whose expires_at has
-- passed. The handler then UPDATEs status='expired' and removes
-- the .part file via os.Remove.
--
-- The status='open' predicate is load-bearing — once a session is
-- committed/cancelled/expired the .part file is already gone and
-- the row is terminal.
SELECT id, part_path
FROM upload_sessions
WHERE status = 'open'
  AND expires_at < now()
ORDER BY expires_at ASC
LIMIT 100;

-- name: ReapStaleUploadPartFiles :many
-- PR-1 fixup #5: sweep .part files for terminal rows whose
-- builderd consumption window has closed. The commit handler
-- leaves .part in place for builderd to consume
-- (pkg/builderd/builderd.go:407 hashFile(SourcePath)); the
-- cancel handler removes its .part at the same time it flips
-- status='cancelled'; but neither has a 1-hour cleanup guarantee
-- for committed rows. This query returns rows in terminal
-- status whose last_patched_at is >1h old and whose part_path
-- cleanup marker is still set; the reaper removes the file and
-- clears the marker after a successful removal.
--
-- The status IN (committed, cancelled, expired) predicate is
-- load-bearing — we never sweep open sessions (could race a
-- PATCH). The last_patched_at < now() - '1 hour' guard stops
-- us from racing a builderd that's mid-consumption right after
-- commit. The 100-row LIMIT bounds the per-tick work the same
-- way ReapExpiredUploadSessions does.
--
-- Index strategy: pg doesn't have an index on (status,
-- last_patched_at) today; the scan is sequential over the
-- terminal-status rows. At expected volumes (≪ 1k terminal
-- rows/day per apid) this is fine. If terminal-row volume
-- grows, add a partial index on (last_patched_at) WHERE
-- status IN ('committed', 'cancelled', 'expired') — leaving
-- as a follow-up ADR rather than conflated into PR-1's
-- migration slot 533.
SELECT id, part_path
FROM upload_sessions
WHERE status IN ('committed', 'cancelled', 'expired')
  AND part_path <> ''
  AND last_patched_at < now() - INTERVAL '1 hour'
ORDER BY last_patched_at ASC
LIMIT 100;

-- name: ClearUploadSessionPartPath :exec
-- Records that the spool file has been removed. Terminal status is
-- required so an out-of-order cleanup call cannot hide the path of
-- an open session that a concurrent PATCH still needs.
UPDATE upload_sessions
   SET part_path = ''
 WHERE id = $1
   AND status IN ('committed', 'cancelled', 'expired')
   AND part_path <> '';

-- name: ExpireUploadSession :exec
-- Marks a single session as expired after the reaper removes its
-- .part file. Split into a separate query from ReapExpiredUploadSessions
-- so the reaper can: (a) scan, (b) delete the file, (c) UPDATE.
-- If (c) fails the row stays at status='open' and the next reaper
-- tick re-runs against it — the file is already gone, so os.Remove
-- returns ErrNotExist and is logged + skipped. This avoids the
-- alternative of a single UPDATE ... RETURNING part_path that
-- would race the file delete across two replicas (single-process
-- for now; future multi-replica deployment needs SELECT ... FOR
-- UPDATE SKIP LOCKED).
UPDATE upload_sessions
   SET status = 'expired'
 WHERE id = $1
   AND status = 'open';

-- name: RecordUploadCommitOutcome :one
-- INSERT ON CONFLICT DO NOTHING for the upload_commit_outcomes
-- companion table. The handler calls this AFTER a successful
-- apidsource.Enqueue and BEFORE writing the 201 response. On
-- retry of POST /v1/uploads/{id}/commit (network blip after the
-- server wrote the row but before the client got the response),
-- the INSERT hits the conflict path and returns 0 rows; the
-- handler then calls GetUploadCommitOutcome to return the
-- original deployment_id. ON CONFLICT DO NOTHING (rather than
-- DO UPDATE) is correct: the original row is canonical.
INSERT INTO upload_commit_outcomes (upload_id, deployment_id, build_id)
VALUES ($1, $2, $3)
ON CONFLICT (upload_id) DO NOTHING
RETURNING upload_id, deployment_id, build_id, finalized_at;

-- name: GetUploadCommitOutcome :one
-- Reads the dedupe row for a retry of POST /v1/uploads/{id}/commit.
-- Returns 0 rows if the original commit never wrote (handler
-- surfaces this as 500 — the prior UPDATE MarkUploadSessionCommitted
-- also failed, so the operator needs the build row's failure
-- class).
SELECT upload_id, deployment_id, build_id, finalized_at
FROM upload_commit_outcomes
WHERE upload_id = $1;

-- name: CountOpenUploadSessionsByAccountApp :one
-- Per-(account_id, app_slug) open-session cap check at the top of
-- POST /v1/uploads. Returns the current count; the handler
-- refuses with 429 upload_session_too_many when count >= 5.
-- Hits the partial index upload_sessions_account_open_idx.
SELECT COUNT(*)::bigint AS count
FROM upload_sessions
WHERE account_id = $1::uuid
  AND app_slug = $2
  AND status = 'open';

-- name: SumOpenUploadSessionBytesByAccount :one
-- Per-account open-spool budget check (4 × SourceTarballMaxMB cap
-- per plan). The handler sums the declared total_size across all
-- open sessions for the account, adds the new total_size, and
-- refuses with 429 upload_session_too_many if the sum exceeds
-- the budget. Hits upload_sessions_account_open_idx for the
-- (account_id) predicate; the SUM is over the partial index.
SELECT COALESCE(SUM(total_size), 0)::bigint AS bytes
FROM upload_sessions
WHERE account_id = $1::uuid
  AND status = 'open';

-- name: ObjectBucketLockApp :one
SELECT id FROM apps WHERE id = $1 AND account_id = $2 AND status <> 'deleted' FOR UPDATE;

-- name: ObjectBucketByName :one
SELECT * FROM object_buckets WHERE app_id = $1 AND account_id = $2 AND name = $3 AND scope = $4 AND state <> 'deleted';

-- name: ObjectBucketCount :one
SELECT count(*) FROM object_buckets WHERE app_id = $1 AND state <> 'deleted';

-- name: ObjectBucketCountForAccount :one
SELECT count(*) FROM object_buckets WHERE account_id = $1 AND state <> 'deleted';

-- name: ObjectBucketPruneTombstones :exec
DELETE FROM object_buckets WHERE account_id = $1 AND state = 'deleted';

-- name: ObjectBucketInsert :one
INSERT INTO object_buckets (id, account_id, app_id, name, scope, region, backend_id, backend_fingerprint, physical_name)
VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9) RETURNING *;

-- name: ObjectBucketList :many
SELECT * FROM object_buckets WHERE account_id = $1 AND app_id = $2 AND state <> 'deleted' ORDER BY created_at, id;

-- name: ObjectBucketGet :one
SELECT * FROM object_buckets WHERE account_id = $1 AND app_id = $2 AND id = $3 AND state <> 'deleted';

-- name: ObjectBucketAccessGrantList :many
SELECT g.account_id, g.bucket_id, g.api_key_id, g.permission,
       coalesce(k.label, '')::text AS key_label, k.status AS key_status,
       g.created_at, g.updated_at
FROM object_storage_access_grants g
JOIN api_keys k ON k.id = g.api_key_id AND k.account_id = g.account_id
JOIN object_buckets b ON b.id = g.bucket_id AND b.account_id = g.account_id
WHERE g.account_id = $1 AND g.bucket_id = $2 AND b.state <> 'deleted'
ORDER BY g.created_at, g.api_key_id;

-- name: ObjectBucketAccessGrantGet :one
SELECT g.account_id, g.bucket_id, g.api_key_id, g.permission,
       coalesce(k.label, '')::text AS key_label, k.status AS key_status,
       g.created_at, g.updated_at
FROM object_storage_access_grants g
JOIN api_keys k ON k.id = g.api_key_id AND k.account_id = g.account_id
JOIN object_buckets b ON b.id = g.bucket_id AND b.account_id = g.account_id
WHERE g.account_id = $1 AND g.bucket_id = $2 AND g.api_key_id = $3
  AND b.state <> 'deleted';

-- name: ObjectBucketAccessGrantUpsert :execrows
INSERT INTO object_storage_access_grants (account_id, bucket_id, api_key_id, permission)
SELECT b.account_id, b.id, k.id, sqlc.arg(permission)::text
FROM object_buckets b
JOIN api_keys k ON k.account_id = b.account_id
WHERE b.account_id = sqlc.arg(account_id) AND b.id = sqlc.arg(bucket_id)
  AND b.state <> 'deleted' AND k.id = sqlc.arg(api_key_id)
  AND k.status IN ('active', 'grace')
  AND NOT ('admin' = ANY(k.scopes))
  AND (sqlc.arg(permission)::text <> 'read' OR k.scopes @> ARRAY['storage:read']::text[])
  AND (sqlc.arg(permission)::text <> 'write' OR k.scopes @> ARRAY['storage:write']::text[])
  AND (sqlc.arg(permission)::text <> 'read_write' OR k.scopes @> ARRAY['storage:read', 'storage:write']::text[])
ON CONFLICT (bucket_id, api_key_id) DO UPDATE
SET permission = EXCLUDED.permission, updated_at = now();

-- name: ObjectBucketAccessGrantDelete :execrows
DELETE FROM object_storage_access_grants g
USING object_buckets b, api_keys k
WHERE g.account_id = $1 AND g.bucket_id = $2 AND g.api_key_id = $3
  AND b.id = g.bucket_id AND b.account_id = g.account_id AND b.state <> 'deleted'
  AND k.id = g.api_key_id AND k.account_id = g.account_id;

-- name: ObjectBucketAccessCheck :one
SELECT EXISTS (
    SELECT 1
    FROM object_storage_access_grants g
    JOIN object_buckets b ON b.id = g.bucket_id AND b.account_id = g.account_id
    JOIN api_keys k ON k.id = g.api_key_id AND k.account_id = g.account_id
    WHERE g.account_id = $1 AND g.bucket_id = $2 AND g.api_key_id = $3
      AND b.state <> 'deleted' AND k.status IN ('active', 'grace')
      AND (($4::text = 'read' AND g.permission IN ('read', 'read_write'))
        OR ($4::text = 'write' AND g.permission IN ('write', 'read_write')))
      AND (($4::text = 'read' AND k.scopes @> ARRAY['storage:read']::text[])
        OR ($4::text = 'write' AND k.scopes @> ARRAY['storage:write']::text[]))
) AS allowed;

-- name: ObjectBucketListForKey :many
SELECT b.*
FROM object_buckets b
JOIN object_storage_access_grants g
  ON g.bucket_id = b.id AND g.account_id = b.account_id
JOIN api_keys k ON k.id = g.api_key_id AND k.account_id = g.account_id
WHERE b.account_id = $1 AND b.app_id = $2 AND g.api_key_id = $3
  AND b.state <> 'deleted' AND k.status IN ('active', 'grace')
ORDER BY b.created_at, b.id;

-- name: ObjectBucketClaim :one
UPDATE object_buckets SET state = $1, lease_token = $2, lease_until = now() + ($3::int * interval '1 second'), updated_at = now(),
attempt_count = CASE WHEN state <> $1 THEN 1 ELSE least(attempt_count + 1, 30) END,
last_error_code = CASE WHEN state <> $1 THEN '' ELSE last_error_code END, retry_at = now()
WHERE object_buckets.account_id = $4 AND object_buckets.app_id = $5 AND object_buckets.id = $6
AND object_buckets.state <> 'deleted' AND (object_buckets.lease_until IS NULL OR object_buckets.lease_until < now())
AND ($1 = 'deleting' OR object_buckets.state = 'provisioning')
AND ($1 <> 'deleting' OR NOT EXISTS (
  SELECT 1 FROM object_storage_multipart_uploads m WHERE m.bucket_id = object_buckets.id
  AND m.state IN ('initiating','active','completing','aborting')
))
AND (NOT sqlc.arg(recovery)::boolean OR object_buckets.state = $1)
AND (object_buckets.retry_at <= now() OR object_buckets.state <> $1) RETURNING *;

-- name: ObjectBucketFinish :execrows
UPDATE object_buckets SET state = $1, lease_token = NULL, lease_until = NULL, updated_at = now(),
attempt_count = 0, last_error_code = '', retry_at = now() WHERE id = $2 AND lease_token = $3;

-- name: ObjectBucketRetry :execrows
UPDATE object_buckets SET lease_token = NULL, lease_until = NULL, updated_at = now(),
last_error_code = $3, retry_at = now() + ($4::int * interval '1 second')
WHERE id = $1 AND lease_token = $2 AND state IN ('provisioning', 'deleting');

-- name: ObjectBucketsDue :many
SELECT * FROM object_buckets
WHERE (state = 'deleting' OR (sqlc.arg(include_provisioning)::boolean AND state = 'provisioning'))
AND retry_at <= now() AND (lease_until IS NULL OR lease_until < now())
ORDER BY retry_at, id LIMIT sqlc.arg(batch_limit)::int;

-- name: SnapshotLocalityNodes :many
SELECT node_id::text AS node_id, true AS is_origin
FROM snapshot_origins
WHERE snapshot_id = $1::uuid AND node_id IS NOT NULL
UNION ALL
SELECT node_id::text AS node_id, false AS is_origin
FROM snapshot_replicas
WHERE snapshot_id = $1::uuid AND state = 'ready'
ORDER BY node_id, is_origin DESC;

-- name: ObjectUsageLockAccount :one
SELECT id FROM accounts WHERE id = $1 FOR UPDATE;

-- name: ObjectUsageBucketAccount :one
SELECT account_id FROM object_buckets WHERE id=$1;

-- name: ObjectUsageBuckets :many
SELECT b.*, u.baseline_bytes, u.baseline_keys, u.granted_bytes, u.granted_keys,
u.observed_bytes, u.observed_keys, u.observed_at, u.attempt_at, u.lease_until AS inventory_lease_until, u.token
FROM object_buckets b LEFT JOIN object_storage_bucket_usage u ON u.bucket_id = b.id
WHERE b.account_id = $1;

-- name: ObjectUsageGrant :one
SELECT max_bytes FROM object_storage_key_grants WHERE bucket_id = $1 AND key_hash = $2;

-- name: ObjectUsageGrantUpsert :exec
INSERT INTO object_storage_key_grants (bucket_id, key_hash, max_bytes) VALUES ($1,$2,$3)
ON CONFLICT (bucket_id,key_hash) DO UPDATE SET max_bytes = greatest(object_storage_key_grants.max_bytes, EXCLUDED.max_bytes);

-- name: ObjectUsageGrantIncrement :exec
UPDATE object_storage_bucket_usage SET granted_bytes = granted_bytes + $2, granted_keys = granted_keys + $3 WHERE bucket_id = $1;

-- name: ObjectUsageAuthorizationCount :one
SELECT count FROM object_storage_authorizations WHERE account_id=$1 AND period_start=$2;

-- name: ObjectUsageAuthorize :exec
INSERT INTO object_storage_authorizations (account_id, period_start, count) VALUES ($1,$2,1)
ON CONFLICT (account_id,period_start) DO UPDATE SET count = object_storage_authorizations.count + 1;

-- name: ObjectUsageReports :many
SELECT r.* FROM object_storage_usage_heads h JOIN object_storage_usage_reports r
USING (account_id,backend_id,period_start,observed_at)
WHERE h.account_id=$1 AND h.period_start=$2 ORDER BY h.backend_id;

-- name: ObjectUsageReportHead :exec
INSERT INTO object_storage_usage_heads (account_id,backend_id,period_start,observed_at) VALUES ($1,$2,$3,$4)
ON CONFLICT (account_id,period_start,backend_id) DO UPDATE SET observed_at=EXCLUDED.observed_at;

-- name: ObjectUsageReportGet :one
SELECT * FROM object_storage_usage_reports WHERE account_id=$1 AND backend_id=$2 AND period_start=$3 AND observed_at=$4;

-- name: ObjectUsageReportInsert :exec
INSERT INTO object_storage_usage_reports (account_id,backend_id,backend_fingerprint,source,period_start,observed_at,stored_byte_hours,request_count,egress_bytes,cost_millicents)
VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10);

-- name: ObjectInventoriesDue :many
SELECT b.* FROM object_buckets b LEFT JOIN object_storage_bucket_usage u ON u.bucket_id=b.id
WHERE b.state='ready' AND (u.attempt_at IS NULL OR u.attempt_at < now() - interval '5 minutes')
AND (u.lease_until IS NULL OR u.lease_until < now())
ORDER BY u.attempt_at NULLS FIRST, b.id LIMIT $1;

-- name: ObjectInventoryClaim :execrows
INSERT INTO object_storage_bucket_usage (bucket_id, attempt_at, lease_until, token)
SELECT id, now(), now()+interval '2 minutes', sqlc.arg(token)::text FROM object_buckets WHERE id=$1 AND state='ready'
ON CONFLICT (bucket_id) DO UPDATE SET attempt_at=now(),lease_until=now()+interval '2 minutes',token=EXCLUDED.token
WHERE object_storage_bucket_usage.lease_until IS NULL OR object_storage_bucket_usage.lease_until < now();

-- name: ObjectInventoryFinish :execrows
UPDATE object_storage_bucket_usage SET baseline_bytes = CASE WHEN observed_at IS NULL THEN sqlc.arg(bytes)::bigint ELSE baseline_bytes END,
baseline_keys = CASE WHEN observed_at IS NULL THEN sqlc.arg(objects)::bigint ELSE baseline_keys END,
observed_bytes=sqlc.arg(bytes),observed_keys=sqlc.arg(objects),observed_at=attempt_at,lease_until=NULL,token=''
WHERE bucket_id=$1 AND token=$2 AND lease_until > now()
AND EXISTS (SELECT 1 FROM object_buckets WHERE id=$1 AND state='ready');

-- name: ObjectInventorySample :exec
INSERT INTO object_storage_inventory_samples (token,bucket_id,observed_at,bytes,objects)
SELECT $2,u.bucket_id,u.observed_at,u.observed_bytes,u.observed_keys FROM object_storage_bucket_usage u WHERE u.bucket_id=$1;

-- name: ObjectMultipartByKey :one
SELECT * FROM object_storage_multipart_uploads
WHERE account_id=$1 AND app_id=$2 AND bucket_id=$3 AND object_key=$4
AND state IN ('initiating','active','completing','aborting');

-- name: ObjectMultipartLockBucket :one
SELECT id FROM object_buckets
WHERE id=$1 AND account_id=$2 AND app_id=$3 AND state='ready' FOR UPDATE;

-- name: ObjectMultipartCount :one
SELECT count(*) FROM object_storage_multipart_uploads
WHERE bucket_id=$1 AND state IN ('initiating','active','completing','aborting');

-- name: ObjectMultipartInsert :one
INSERT INTO object_storage_multipart_uploads
(id,account_id,app_id,bucket_id,object_key,size_bytes,part_size_bytes,part_count,content_type,expires_at)
VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10) RETURNING *;

-- name: ObjectMultipartGet :one
SELECT * FROM object_storage_multipart_uploads
WHERE account_id=$1 AND app_id=$2 AND bucket_id=$3 AND id=$4;

-- name: ObjectMultipartList :many
SELECT * FROM object_storage_multipart_uploads
WHERE account_id=$1 AND app_id=$2 AND bucket_id=$3 AND id>$4
ORDER BY id LIMIT sqlc.arg(page_limit)::int;

-- name: ObjectMultipartClaim :one
UPDATE object_storage_multipart_uploads SET
state=sqlc.arg(operation), lease_token=sqlc.arg(token),
lease_until=now()+(sqlc.arg(lease_seconds)::int * interval '1 second'),
completion_parts=CASE WHEN state='active' AND sqlc.arg(operation)::text='completing'
  THEN sqlc.arg(completion_parts)::jsonb ELSE completion_parts END,
attempt_count=CASE WHEN state<>sqlc.arg(operation)::text THEN 1 ELSE least(attempt_count+1,30) END,
last_error_code=CASE WHEN state<>sqlc.arg(operation)::text THEN '' ELSE last_error_code END,
retry_at=now(), updated_at=now()
WHERE account_id=sqlc.arg(account_id) AND app_id=sqlc.arg(app_id)
AND bucket_id=sqlc.arg(bucket_id) AND id=sqlc.arg(id)
AND (lease_until IS NULL OR lease_until<now())
AND (retry_at<=now() OR state<>sqlc.arg(operation)::text)
AND (NOT sqlc.arg(recovery)::boolean OR state=sqlc.arg(operation)::text
  OR (state='active' AND sqlc.arg(operation)::text='aborting'))
AND (
  (sqlc.arg(operation)::text='initiating' AND state='initiating' AND provider_upload_id='') OR
  (sqlc.arg(operation)::text='completing' AND state IN ('active','completing')
    AND (state<>'active' OR expires_at>now())
    AND (state<>'active' OR jsonb_array_length(sqlc.arg(completion_parts)::jsonb)>0)) OR
  (sqlc.arg(operation)::text='aborting' AND state IN ('active','aborting') AND provider_upload_id<>'')
) RETURNING *;

-- name: ObjectMultipartActivate :execrows
UPDATE object_storage_multipart_uploads SET state='active',provider_upload_id=$3,
lease_token=NULL,lease_until=NULL,attempt_count=0,last_error_code='',retry_at=now(),updated_at=now()
WHERE id=$1 AND lease_token=$2 AND state='initiating' AND $3<>'';

-- name: ObjectMultipartFinish :execrows
UPDATE object_storage_multipart_uploads SET state=$3,lease_token=NULL,lease_until=NULL,
attempt_count=0,last_error_code='',retry_at=now(),updated_at=now()
WHERE id=$1 AND lease_token=$2 AND
((state='completing' AND $3='completed') OR (state='aborting' AND $3='aborted'));

-- name: ObjectMultipartRetry :execrows
UPDATE object_storage_multipart_uploads SET lease_token=NULL,lease_until=NULL,last_error_code=$3,
retry_at=now()+($4::int * interval '1 second'),updated_at=now()
WHERE id=$1 AND lease_token=$2 AND state IN ('initiating','completing','aborting');

-- name: ObjectMultipartDue :many
SELECT * FROM object_storage_multipart_uploads
WHERE (((state IN ('initiating','completing','aborting')) AND retry_at<=now())
  OR (state='active' AND expires_at<=now()))
AND (lease_until IS NULL OR lease_until<now())
ORDER BY retry_at,id LIMIT sqlc.arg(batch_limit)::int;

-- name: SnapshotStorageKeys :many
SELECT storage_key FROM snapshots WHERE deployment_id = $1;
