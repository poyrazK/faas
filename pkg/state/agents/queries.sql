-- name: LockAccountAgents :exec
-- Serializes every agent write and run admission of one account (ADR-20261027090010 §4).
SELECT pg_advisory_xact_lock(hashtextextended('agent-runs:' || sqlc.arg(account_id)::text, 0));

-- name: CountActiveAgents :one
SELECT count(*)::integer FROM agents WHERE account_id=sqlc.arg(account_id)::text::uuid AND state='active';

-- name: InsertAgent :one
INSERT INTO agents (account_id,name,description)
 VALUES (sqlc.arg(account_id)::text::uuid,sqlc.arg(name)::text,sqlc.arg(description)::text)
 RETURNING id::text AS id,name,description,state,latest_version,created_at,updated_at;

-- name: GetActiveAgent :one
SELECT id::text AS id,name,description,state,latest_version,created_at,updated_at FROM agents
 WHERE account_id=sqlc.arg(account_id)::text::uuid AND name=sqlc.arg(name)::text AND state='active';

-- name: ListActiveAgents :many
SELECT id::text AS id,name,description,state,latest_version,created_at,updated_at FROM agents
 WHERE account_id=sqlc.arg(account_id)::text::uuid AND state='active' AND name>sqlc.arg(after)::text
 ORDER BY name LIMIT sqlc.arg(max_rows)::integer;

-- name: BumpAgentVersion :one
UPDATE agents SET latest_version=latest_version+1, updated_at=now()
 WHERE id=sqlc.arg(agent_id)::text::uuid AND account_id=sqlc.arg(account_id)::text::uuid AND state='active'
 RETURNING latest_version;

-- name: MarkAgentDeleted :execrows
UPDATE agents SET state='deleted', deleted_at=now(), updated_at=now()
 WHERE id=sqlc.arg(agent_id)::text::uuid AND account_id=sqlc.arg(account_id)::text::uuid AND state='active';

-- name: InsertAgentVersion :one
INSERT INTO agent_versions (agent_id,version,account_id,harness_preset,harness_image,harness_run,harness_models,prompt_template,ram_mb,timeout_sec,
  model_integrations,ai_budget_millicents,deliveries,pause_during_model_calls,capability_policy)
 VALUES (sqlc.arg(agent_id)::text::uuid,sqlc.arg(version)::integer,sqlc.arg(account_id)::text::uuid,
  sqlc.narg(harness_preset)::text,sqlc.narg(harness_image)::text,sqlc.arg(harness_run)::text[],sqlc.arg(harness_models)::jsonb,
  sqlc.arg(prompt_template)::text,sqlc.narg(ram_mb)::integer,sqlc.arg(timeout_sec)::integer,
  sqlc.arg(model_integrations)::jsonb,sqlc.narg(ai_budget_millicents)::bigint,sqlc.arg(deliveries)::jsonb,
  sqlc.arg(pause_during_model_calls)::boolean,sqlc.narg(capability_policy)::jsonb)
 RETURNING version,harness_preset,harness_image,harness_run,harness_models,prompt_template,ram_mb,timeout_sec,created_at,
  model_integrations,ai_budget_millicents,deliveries,pause_during_model_calls,capability_policy;

-- name: GetAgentVersion :one
SELECT version,harness_preset,harness_image,harness_run,harness_models,prompt_template,ram_mb,timeout_sec,created_at,
  model_integrations,ai_budget_millicents,deliveries,pause_during_model_calls,capability_policy FROM agent_versions
 WHERE agent_id=sqlc.arg(agent_id)::text::uuid AND account_id=sqlc.arg(account_id)::text::uuid AND version=sqlc.arg(version)::integer;

-- name: ListAgentModelIntegrations :many
-- The account's enabled customer AI integrations among ids (ADR-20261027090010 §7).
SELECT id::text AS id, ai_provider::text AS ai_provider FROM outbound_integrations
 WHERE account_id=sqlc.arg(account_id)::text::uuid AND id::text=ANY(sqlc.arg(ids)::text[]) AND enabled AND owner_kind='customer'
  AND ai_provider IS NOT NULL;

-- name: CountLiveAgentRuns :one
SELECT count(*)::integer FROM agent_runs
 WHERE agent_id=sqlc.arg(agent_id)::text::uuid AND account_id=sqlc.arg(account_id)::text::uuid
 AND state IN ('queued','starting','running','delivering');

-- name: GetTenantStatus :one
SELECT status FROM platform_tenants WHERE id=sqlc.arg(tenant_id)::text::uuid AND account_id=sqlc.arg(account_id)::text::uuid;

-- name: FindDedupedRun :one
SELECT id::text AS id FROM agent_runs
 WHERE agent_id=sqlc.arg(agent_id)::text::uuid AND tenant_id IS NOT DISTINCT FROM sqlc.narg(tenant_id)::text::uuid
 AND dedupe_key=sqlc.arg(dedupe_key)::text;

-- name: CountAgentRunsToday :one
-- Runs admitted since the start of the database's current UTC day.
SELECT count(*)::integer FROM agent_runs
 WHERE account_id=sqlc.arg(account_id)::text::uuid
 AND queued_at>=(date_trunc('day', now() AT TIME ZONE 'UTC') AT TIME ZONE 'UTC');

-- name: CountQueuedAgentRuns :one
SELECT count(*)::integer FROM agent_runs WHERE account_id=sqlc.arg(account_id)::text::uuid AND state='queued';

-- name: InsertAgentRun :one
INSERT INTO agent_runs (account_id,agent_id,agent_version,tenant_id,trigger,dedupe_key,prompt,inputs,ram_mb,timeout_sec,
  checkout_repository,checkout_commit,checkout_installation_id,github_repository,github_number,github_installation_id,
  workflow_run_id,workflow_step,workflow_event)
 VALUES (sqlc.arg(account_id)::text::uuid,sqlc.arg(agent_id)::text::uuid,sqlc.arg(agent_version)::integer,sqlc.narg(tenant_id)::text::uuid,
  sqlc.arg(trigger)::text,sqlc.narg(dedupe_key)::text,sqlc.narg(prompt)::text,sqlc.arg(inputs)::jsonb,sqlc.narg(ram_mb)::integer,sqlc.arg(timeout_sec)::integer,
  sqlc.narg(checkout_repository)::text,sqlc.narg(checkout_commit)::text,sqlc.narg(checkout_installation_id)::bigint,
  sqlc.narg(github_repository)::text,sqlc.narg(github_number)::bigint,sqlc.narg(github_installation_id)::bigint,
  sqlc.narg(workflow_run_id)::text::uuid,sqlc.narg(workflow_step)::text,sqlc.narg(workflow_event)::text)
 RETURNING id::text AS id;

-- name: ResolveGitHubInstallation :one
-- The account's named installation, or its most recent one (ADR-20261027090010 §3).
SELECT installation_id FROM github_installations
 WHERE account_id=sqlc.arg(account_id)::text::uuid
  AND (sqlc.narg(installation_id)::bigint IS NULL OR installation_id=sqlc.narg(installation_id)::bigint)
 ORDER BY sealed_at DESC LIMIT 1;

-- name: GetAgentRun :one
SELECT r.id::text AS id,a.name AS agent_name,r.agent_id::text AS agent_id,r.agent_version,coalesce(r.tenant_id::text, '')::text AS tenant_id,r.trigger,r.dedupe_key,
  r.prompt,r.inputs,r.state,r.ram_mb,r.timeout_sec,r.attempt,r.cancel_requested_at,r.outcome_code,r.outcome_message,
  r.queued_at,r.started_at,r.running_at,r.finished_at,r.updated_at,
  r.checkout_repository,r.checkout_commit,r.checkout_installation_id,
  r.exit_code,r.github_repository,r.github_number,r.github_installation_id
 FROM agent_runs r JOIN agents a ON a.id=r.agent_id
 WHERE r.id=sqlc.arg(run_id)::text::uuid AND r.account_id=sqlc.arg(account_id)::text::uuid;

-- name: ListAgentRuns :many
-- Newest first, keyset-paged on (queued_at, id).
SELECT r.id::text AS id,a.name AS agent_name,r.agent_id::text AS agent_id,r.agent_version,coalesce(r.tenant_id::text, '')::text AS tenant_id,r.trigger,r.dedupe_key,
  r.prompt,r.inputs,r.state,r.ram_mb,r.timeout_sec,r.attempt,r.cancel_requested_at,r.outcome_code,r.outcome_message,
  r.queued_at,r.started_at,r.running_at,r.finished_at,r.updated_at,
  r.checkout_repository,r.checkout_commit,r.checkout_installation_id,
  r.exit_code,r.github_repository,r.github_number,r.github_installation_id
 FROM agent_runs r JOIN agents a ON a.id=r.agent_id
 WHERE r.account_id=sqlc.arg(account_id)::text::uuid
 AND (sqlc.narg(agent_id)::text::uuid IS NULL OR r.agent_id=sqlc.narg(agent_id)::text::uuid)
 AND (sqlc.narg(state)::text IS NULL OR r.state=sqlc.narg(state)::text)
 AND (sqlc.narg(tenant_id)::text::uuid IS NULL OR r.tenant_id=sqlc.narg(tenant_id)::text::uuid)
 AND (sqlc.narg(before_queued_at)::timestamptz IS NULL
      OR (r.queued_at,r.id)<(sqlc.narg(before_queued_at)::timestamptz,sqlc.narg(before_id)::text::uuid))
 ORDER BY r.queued_at DESC, r.id DESC LIMIT sqlc.arg(max_rows)::integer;

-- name: CancelQueuedAgentRun :execrows
UPDATE agent_runs SET state='cancelled', cancel_requested_at=now(), finished_at=now(), outcome_code='cancelled', updated_at=now()
 WHERE id=sqlc.arg(run_id)::text::uuid AND account_id=sqlc.arg(account_id)::text::uuid AND state='queued';

-- name: RequestAgentRunCancel :execrows
-- agentd stops a started run after a short grace period (ADR-20261027090010 §4).
UPDATE agent_runs SET cancel_requested_at=coalesce(cancel_requested_at,now()), updated_at=now()
 WHERE id=sqlc.arg(run_id)::text::uuid AND account_id=sqlc.arg(account_id)::text::uuid
 AND state IN ('starting','running','delivering');

-- agentd (ADR-20261027090014 §1) owns every transition after queued.

-- name: ListClaimableAgentRuns :many
-- Oldest first. Rows another replica holds are skipped, not waited on.
SELECT id::text AS id, account_id::text AS account_id FROM agent_runs
 WHERE state='queued' ORDER BY queued_at, id LIMIT sqlc.arg(max_rows)::integer FOR UPDATE SKIP LOCKED;

-- name: AgentRunQueueStats :one
-- Every account's queued runs and how long the oldest has waited, for
-- agentd's queue gauges. agent_runs_queue_idx covers it.
SELECT count(*)::integer AS queued,
  coalesce(extract(epoch FROM now()-min(queued_at)), 0)::float8 AS oldest_seconds
 FROM agent_runs WHERE state='queued';

-- name: TryLockAccountAgents :one
-- The same per-account key apid admits under; agentd never waits for it, so
-- an apid transaction that holds it and wants a run row cannot deadlock.
SELECT pg_try_advisory_xact_lock(hashtextextended('agent-runs:' || sqlc.arg(account_id)::text, 0))::boolean;

-- name: AccountAgentLoad :one
-- Live sandbox sessions share the account's concurrency with live runs
-- (ADR-20261027090013 §7).
SELECT a.plan,
  ((SELECT count(*) FROM agent_runs r WHERE r.account_id=a.id AND r.state IN ('starting','running','delivering'))
   + (SELECT count(*) FROM sandbox_sessions sb WHERE sb.account_id=a.id AND sb.state IN ('starting','ready','frozen')))::integer AS live
 FROM accounts a WHERE a.id=sqlc.arg(account_id)::text::uuid;

-- name: StartAgentRun :execrows
UPDATE agent_runs SET state='starting', lease_token=sqlc.arg(lease_token)::text, lease_owner=sqlc.arg(lease_owner)::text,
  lease_expires_at=now()+make_interval(secs=>sqlc.arg(ttl_seconds)::double precision), started_at=now(), updated_at=now()
 WHERE id=sqlc.arg(run_id)::text::uuid AND state='queued';

-- name: GetAgentRunClaim :one
SELECT r.id::text AS id, r.account_id::text AS account_id, a.plan, r.agent_id::text AS agent_id, ag.name AS agent_name,
  r.agent_version, coalesce(r.tenant_id::text, '')::text AS tenant_id, r.prompt, r.inputs, r.ram_mb, r.timeout_sec, r.attempt,
  v.harness_preset, v.harness_image, v.harness_run, v.harness_models, v.prompt_template, v.model_integrations, v.ai_budget_millicents,
  r.checkout_repository, r.checkout_commit, r.checkout_installation_id, r.queued_at, v.pause_during_model_calls,
  v.capability_policy
 FROM agent_runs r JOIN accounts a ON a.id=r.account_id JOIN agents ag ON ag.id=r.agent_id
  JOIN agent_versions v ON v.agent_id=r.agent_id AND v.version=r.agent_version
 WHERE r.id=sqlc.arg(run_id)::text::uuid;

-- name: MarkAgentRunRunning :execrows
-- The at-most-once fence (ADR-20261027090010 §4): from here a lost lease fails the run.
UPDATE agent_runs SET state='running', running_at=now(), node_id=sqlc.arg(node_id)::text, instance_id=sqlc.arg(instance_id)::text,
  ai_budget_millicents=sqlc.arg(ai_budget_millicents)::bigint,
  deadline_credit_max_sec=sqlc.arg(deadline_credit_max_sec)::integer, updated_at=now()
 WHERE id=sqlc.arg(run_id)::text::uuid AND lease_token=sqlc.arg(lease_token)::text AND state='starting';

-- name: ListAgentRunApprovalWaits :many
-- ADR-20261027090016 amendment 3: each approval waits from its request
-- until its decision, its expiry or outboundd's release, whichever is
-- first; one still held waits until now. Fenced by the run's lease.
SELECT p.created_at,
       least(coalesce(p.released_at, now()), p.expires_at, coalesce(d.decided_at, 'infinity'::timestamptz))::timestamptz AS ended_at
  FROM agent_run_approvals p
  JOIN agent_runs r ON r.id = p.run_id
  LEFT JOIN agent_run_approval_decisions d ON d.approval_id = p.id
 WHERE p.run_id = sqlc.arg(run_id)::text::uuid AND r.lease_token = sqlc.arg(lease_token)::text
 ORDER BY p.created_at
 LIMIT sqlc.arg(max_rows)::integer;

-- name: RecordAgentRunBoot :execrows
UPDATE agent_runs SET boot_ms=sqlc.arg(boot_ms)::integer, updated_at=now()
 WHERE id=sqlc.arg(run_id)::text::uuid AND lease_token=sqlc.arg(lease_token)::text AND state='running';

-- name: RecordAgentRunUsage :execrows
-- ADR-20261027090011: vmmd's measured memory of the run VM. Only the first
-- record of a run is kept.
INSERT INTO agent_run_usage (run_id, account_id, method, samples, expected_samples, measured_ms, paused_ms,
    private_mb_ms, unpaused_private_mb_ms, billable_mb_ms, peak_private_mb, cold_boot)
SELECT r.id, r.account_id, sqlc.arg(method)::smallint, sqlc.arg(samples)::integer, sqlc.arg(expected_samples)::integer,
       sqlc.arg(measured_ms)::bigint, sqlc.arg(paused_ms)::bigint, sqlc.arg(private_mb_ms)::bigint,
       sqlc.arg(unpaused_private_mb_ms)::bigint, sqlc.arg(billable_mb_ms)::bigint, sqlc.arg(peak_private_mb)::bigint,
       sqlc.arg(cold_boot)::boolean
  FROM agent_runs r
 WHERE r.id=sqlc.arg(run_id)::text::uuid AND r.lease_token=sqlc.arg(lease_token)::text AND r.state='running'
ON CONFLICT (run_id) DO NOTHING;

-- name: GetAgentRunUsage :one
SELECT method, samples, expected_samples, measured_ms, paused_ms, private_mb_ms, unpaused_private_mb_ms,
       billable_mb_ms, peak_private_mb, cold_boot
  FROM agent_run_usage WHERE run_id=sqlc.arg(run_id)::text::uuid AND account_id=sqlc.arg(account_id)::text::uuid;

-- name: AgentVersionPrivatePeak :one
-- ADR-20261027090015 §3: the p95 private peak of a version's recent measured
-- runs of one input class, leaving out runs with too many missing samples.
SELECT count(*)::integer AS runs,
       coalesce(percentile_disc(0.95) WITHIN GROUP (ORDER BY h.peak_private_mb), 0)::bigint AS peak_mb
  FROM (SELECT u.peak_private_mb
          FROM agent_run_usage u JOIN agent_runs r ON r.id = u.run_id
         WHERE u.account_id = sqlc.arg(account_id)::text::uuid AND r.agent_id = sqlc.arg(agent_id)::text::uuid
           AND r.agent_version = sqlc.arg(agent_version)::integer
           AND (r.checkout_repository IS NOT NULL) = sqlc.arg(checkout)::boolean
           AND u.samples::bigint * 100 >= u.expected_samples::bigint * (100 - sqlc.arg(max_gap_percent)::integer)
         ORDER BY u.recorded_at DESC
         LIMIT sqlc.arg(window_runs)::integer) h;

-- name: RenewAgentRunLease :one
UPDATE agent_runs SET lease_expires_at=now()+make_interval(secs=>sqlc.arg(ttl_seconds)::double precision), updated_at=now()
 WHERE id=sqlc.arg(run_id)::text::uuid AND lease_token=sqlc.arg(lease_token)::text AND state IN ('starting','running','delivering')
 RETURNING (cancel_requested_at IS NOT NULL)::boolean AS cancel_requested;

-- name: RequeueAgentRun :execrows
-- Only a starting run, which reached no VM, goes back to the queue.
UPDATE agent_runs SET state='queued', lease_token=NULL, lease_expires_at=NULL, lease_owner=NULL, started_at=NULL, updated_at=now()
 WHERE id=sqlc.arg(run_id)::text::uuid AND lease_token=sqlc.arg(lease_token)::text AND state='starting';

-- name: FinishAgentRun :execrows
UPDATE agent_runs SET state=sqlc.arg(state)::text, outcome_code=sqlc.narg(outcome_code)::text,
  outcome_message=sqlc.narg(outcome_message)::text, exit_code=sqlc.narg(exit_code)::integer, signal=sqlc.narg(signal)::integer,
  finished_at=now(), lease_token=NULL, lease_expires_at=NULL, updated_at=now()
 WHERE id=sqlc.arg(run_id)::text::uuid AND lease_token=sqlc.arg(lease_token)::text AND state IN ('starting','running','delivering');

-- name: InsertAgentRunOutputs :exec
INSERT INTO agent_run_outputs (run_id, account_id, result, files, log_tail, log_truncated)
 VALUES (sqlc.arg(run_id)::text::uuid, sqlc.arg(account_id)::text::uuid, sqlc.narg(result)::jsonb, sqlc.arg(files)::jsonb,
  sqlc.arg(log_tail)::text, sqlc.arg(log_truncated)::boolean)
 ON CONFLICT (run_id) DO NOTHING;

-- name: RequeueExpiredStartingAgentRuns :execrows
UPDATE agent_runs SET state='queued', lease_token=NULL, lease_expires_at=NULL, lease_owner=NULL, started_at=NULL, updated_at=now()
 WHERE state='starting' AND lease_expires_at < now();

-- name: FailExpiredRunningAgentRuns :many
UPDATE agent_runs SET state='failed', outcome_code='lease_lost',
  outcome_message='agentd lost the lease while the run was running; a running run is never replayed',
  finished_at=now(), lease_token=NULL, lease_expires_at=NULL, updated_at=now()
 WHERE state IN ('running','delivering') AND lease_expires_at < now()
 RETURNING id::text AS id, account_id::text AS account_id;

-- name: GetAgentRunOutputs :one
SELECT result, files, log_tail, log_truncated FROM agent_run_outputs
 WHERE run_id=sqlc.arg(run_id)::text::uuid AND account_id=sqlc.arg(account_id)::text::uuid;

-- name: InsertAgentRunFinishedDeliveries :execrows
-- ADR-20261027090010 §8, ADR-224: one pending delivery per enabled account receiver
-- subscribed to agent_run.finished. The ADR-076 dispatcher signs, sends and
-- retries it; the run's finish fence makes this run exactly once.
INSERT INTO app_webhook_deliveries (webhook_id, app_id, account_id, event, payload, source_event_id)
SELECT w.id, NULL, w.account_id, 'agent_run.finished', sqlc.arg(payload)::jsonb, sqlc.arg(run_id)::text::uuid
  FROM app_webhooks w
 WHERE w.account_id=sqlc.arg(account_id)::text::uuid AND w.scope='account' AND w.enabled
  AND 'agent_run.finished'=ANY(w.event_filter);

-- Deliveries (ADR-20261027090010 §8). FinishRun writes a succeeded run's deliveries;
-- agentd's delivery worker (ADR-20261027090014) owns every later delivery transition.

-- name: GetAgentRunDeliveryPlan :one
SELECT v.deliveries,coalesce(r.tenant_id::text, '')::text AS tenant_id,r.agent_id::text AS agent_id,
  r.github_repository,r.github_number,r.github_installation_id
 FROM agent_runs r JOIN agent_versions v ON v.agent_id=r.agent_id AND v.version=r.agent_version
 WHERE r.id=sqlc.arg(run_id)::text::uuid AND r.account_id=sqlc.arg(account_id)::text::uuid;

-- name: EnterAgentRunDelivering :execrows
-- The VM is gone; deliveries hold no lease on the run.
UPDATE agent_runs SET state='delivering', exit_code=sqlc.narg(exit_code)::integer, signal=sqlc.narg(signal)::integer,
  lease_token=NULL, lease_expires_at=NULL, updated_at=now()
 WHERE id=sqlc.arg(run_id)::text::uuid AND lease_token=sqlc.arg(lease_token)::text AND state IN ('starting','running');

-- name: InsertAgentDelivery :exec
INSERT INTO agent_deliveries (account_id,run_id,ordinal,destination,idempotency_key,installation_id,repository,number,
  bucket_id,bucket_app_id,object_prefix,body,body_truncated,state,deadline_at,outcome_code,outcome_message,finished_at)
 VALUES (sqlc.arg(account_id)::text::uuid,sqlc.arg(run_id)::text::uuid,sqlc.arg(ordinal)::smallint,sqlc.arg(destination)::text,
  sqlc.arg(idempotency_key)::text,sqlc.narg(installation_id)::bigint,sqlc.narg(repository)::text,sqlc.narg(number)::bigint,
  sqlc.narg(bucket_id)::text::uuid,sqlc.narg(bucket_app_id)::text::uuid,sqlc.narg(object_prefix)::text,
  sqlc.arg(body)::text,sqlc.arg(body_truncated)::boolean,sqlc.arg(state)::text,now()+make_interval(secs => sqlc.arg(window_seconds)::float8),
  sqlc.narg(outcome_code)::text,sqlc.narg(outcome_message)::text,
  CASE WHEN sqlc.arg(state)::text='pending' THEN NULL ELSE now() END);

-- name: LockDeliveringAgentRun :one
-- Settling locks the run first, so the last two deliveries of a run to end
-- cannot both miss each other.
SELECT account_id::text AS account_id,(cancel_requested_at IS NOT NULL)::boolean AS cancel_requested FROM agent_runs
 WHERE id=sqlc.arg(run_id)::text::uuid AND state='delivering' FOR UPDATE;

-- name: SummarizeAgentRunDeliveries :one
SELECT count(*) FILTER (WHERE state IN ('pending','sending','reconciling'))::integer AS open,
  count(*) FILTER (WHERE state='failed')::integer AS failed,
  count(*) FILTER (WHERE state='outcome_unknown')::integer AS unknown,
  count(*) FILTER (WHERE state='cancelled')::integer AS cancelled,
  coalesce(min(outcome_message) FILTER (WHERE state IN ('failed','outcome_unknown')), '')::text AS message
 FROM agent_deliveries WHERE run_id=sqlc.arg(run_id)::text::uuid;

-- name: FinishDeliveringAgentRun :execrows
UPDATE agent_runs SET state=sqlc.arg(state)::text, outcome_code=sqlc.narg(outcome_code)::text,
  outcome_message=sqlc.narg(outcome_message)::text, finished_at=now(), updated_at=now()
 WHERE id=sqlc.arg(run_id)::text::uuid AND state='delivering';

-- name: ListAgentRunDeliveries :many
SELECT destination,state,attempts,external_url,outcome_code,outcome_message,body_truncated,finished_at FROM agent_deliveries
 WHERE run_id=sqlc.arg(run_id)::text::uuid AND account_id=sqlc.arg(account_id)::text::uuid ORDER BY ordinal;

-- name: SupersedeAgentDeliveries :many
-- A waiting delivery gives way to a newer one for the same destination
-- object that is still open or already delivered: the newer content wins.
UPDATE agent_deliveries d SET state='superseded', outcome_code='superseded',
  outcome_message='a later run''s delivery to the same destination replaced this one', finished_at=now(), updated_at=now()
 WHERE d.state IN ('pending','reconciling') AND EXISTS (SELECT 1 FROM agent_deliveries n
   WHERE n.idempotency_key=d.idempotency_key AND n.state IN ('pending','sending','reconciling','delivered')
   AND (n.created_at,n.id)>(d.created_at,d.id))
 RETURNING d.run_id::text AS run_id,d.destination,d.state;

-- name: CancelAgentDeliveries :many
-- Deliveries never start for a cancelled run (ADR-20261027090010 §4).
UPDATE agent_deliveries d SET state='cancelled', outcome_code='cancelled', outcome_message=NULL, finished_at=now(), updated_at=now()
 FROM agent_runs r WHERE r.id=d.run_id AND r.cancel_requested_at IS NOT NULL AND d.state IN ('pending','reconciling')
 RETURNING d.run_id::text AS run_id,d.destination,d.state;

-- name: ExpireAgentDeliveries :many
-- A send whose worker vanished may have written: it is reconciled, or ends
-- outcome_unknown once the window has closed. A delivery still waiting when
-- the window closes fails.
UPDATE agent_deliveries SET
  state=CASE WHEN deadline_at>now() THEN 'reconciling' WHEN state='pending' THEN 'failed' ELSE 'outcome_unknown' END,
  outcome_code=CASE WHEN deadline_at>now() THEN outcome_code WHEN state='pending' THEN 'delivery_timed_out' ELSE 'outcome_unknown' END,
  outcome_message=CASE WHEN deadline_at>now() THEN outcome_message WHEN state='pending'
    THEN 'the delivery window closed before the delivery succeeded'
    ELSE 'the delivery window closed before the destination confirmed the write' END,
  finished_at=CASE WHEN deadline_at>now() THEN NULL ELSE now() END,
  lease_token=NULL, lease_expires_at=NULL, updated_at=now()
 WHERE (state='sending' AND lease_expires_at<now()) OR (state IN ('pending','reconciling') AND deadline_at<=now())
 RETURNING run_id::text AS run_id,destination,state;

-- name: ClaimAgentDeliveries :many
-- Due deliveries, never two sends for one destination object at once. Rows
-- another replica holds are skipped, not waited on.
UPDATE agent_deliveries SET state='sending', lease_token=sqlc.arg(lease_token)::text,
  lease_expires_at=now()+make_interval(secs => sqlc.arg(ttl_seconds)::float8), attempts=attempts+1, updated_at=now()
 WHERE id IN (SELECT d.id FROM agent_deliveries d
   WHERE d.state IN ('pending','reconciling') AND d.next_attempt_at<=now() AND d.deadline_at>now() AND d.destination<>'email' 
   AND NOT EXISTS (SELECT 1 FROM agent_deliveries o WHERE o.idempotency_key=d.idempotency_key AND o.state='sending')
   ORDER BY d.next_attempt_at, d.id LIMIT sqlc.arg(max_rows)::integer FOR UPDATE SKIP LOCKED)
 RETURNING id::text AS id,account_id::text AS account_id,run_id::text AS run_id,destination,idempotency_key,
  installation_id,repository,number,body,attempts,deadline_at,
  coalesce(bucket_id::text, '')::text AS bucket_id,coalesce(bucket_app_id::text, '')::text AS bucket_app_id,
  coalesce(object_prefix, '')::text AS object_prefix,
  coalesce((SELECT o.files FROM agent_run_outputs o WHERE o.run_id=agent_deliveries.run_id), '[]')::jsonb AS files;

-- name: EndAgentDeliveryAttempt :one
-- Fenced on the attempt's lease: a worker that lost it changes nothing.
UPDATE agent_deliveries SET state=sqlc.arg(state)::text,
  next_attempt_at=now()+make_interval(secs => sqlc.arg(retry_seconds)::float8),
  outcome_code=sqlc.narg(outcome_code)::text, outcome_message=sqlc.narg(outcome_message)::text,
  external_id=coalesce(sqlc.narg(external_id)::text, external_id), external_url=coalesce(sqlc.narg(external_url)::text, external_url),
  finished_at=CASE WHEN sqlc.arg(state)::text IN ('pending','reconciling') THEN NULL ELSE now() END,
  lease_token=NULL, lease_expires_at=NULL, updated_at=now()
 WHERE id=sqlc.arg(delivery_id)::text::uuid AND lease_token=sqlc.arg(lease_token)::text AND state='sending'
 RETURNING run_id::text AS run_id;

-- name: ListSettleableAgentRuns :many
-- A delivering run with no open delivery left; normally settled by the
-- delivery that ended last, this catches any run that was missed.
SELECT r.id::text AS id FROM agent_runs r WHERE r.state='delivering'
 AND NOT EXISTS (SELECT 1 FROM agent_deliveries d WHERE d.run_id=r.id AND d.state IN ('pending','sending','reconciling'))
 ORDER BY r.updated_at LIMIT sqlc.arg(max_rows)::integer;

-- name: ResolveAgentDeliveryBucket :one
-- The app that owns one of the account's buckets, fixed when a version
-- declares a bucket delivery.
SELECT app_id::text AS app_id FROM object_buckets
 WHERE account_id=sqlc.arg(account_id)::text::uuid AND id=sqlc.arg(bucket_id)::text::uuid AND state<>'deleted';

-- name: SettleCapturedEmailDeliveries :many
-- An email delivery ends with apid's mail outbox receipt: queued is
-- delivered, any other disposition fails. agentd only reads the receipt.
UPDATE agent_deliveries d SET state=CASE WHEN o.disposition='queued' THEN 'delivered' ELSE 'failed' END,
  outcome_code=CASE WHEN o.disposition='queued' THEN NULL ELSE 'mail_'||o.disposition END,
  outcome_message=CASE WHEN o.disposition='queued' THEN NULL WHEN o.disposition='unavailable'
    THEN 'the account has no address that receives mail' ELSE 'the mail outbox did not queue the summary' END,
  finished_at=now(), updated_at=now()
 FROM platform_mail_agent_run_observations o
 WHERE o.delivery_id=d.id AND d.destination='email' AND d.state='pending'
 RETURNING d.run_id::text AS run_id,d.destination,d.state;

-- name: ListUndeliveredWorkflowAgentRuns :many
-- ADR-20261027090010 §9: finished runs an automation's agent step admitted whose end
-- schedd has not delivered to the still-active workflow run. The workflow
-- event itself is the delivery receipt.
SELECT r.id::text AS id,r.account_id::text AS account_id,r.workflow_run_id::text AS workflow_run_id,
  r.workflow_step::text AS workflow_step,r.workflow_event::text AS workflow_event,r.state,r.outcome_code,r.outcome_message,o.result
 FROM agent_runs r LEFT JOIN agent_run_outputs o ON o.run_id=r.id
 WHERE r.workflow_run_id IS NOT NULL AND r.finished_at IS NOT NULL AND r.finished_at>now()-interval '7 days'
 AND EXISTS (SELECT 1 FROM workflow_runs w WHERE w.id=r.workflow_run_id AND w.status IN ('pending','running','awaiting_event'))
 AND NOT EXISTS (SELECT 1 FROM workflow_events e WHERE e.run_id=r.workflow_run_id AND e.event_name=r.workflow_event)
 ORDER BY r.finished_at,r.id LIMIT sqlc.arg(max_rows)::integer;

-- name: RevokeAgentCallbacks :one
-- ADR-20261030125501 §3: revoke every callback URL of a thread issued until now.
INSERT INTO agent_callback_revocations (endpoint_id, thread_id, account_id, revoked_at)
VALUES (sqlc.arg(endpoint_id)::text::uuid, sqlc.arg(thread_id)::text, sqlc.arg(account_id)::text::uuid, clock_timestamp())
ON CONFLICT (endpoint_id, thread_id) DO UPDATE SET revoked_at = clock_timestamp(), account_id = excluded.account_id
RETURNING revoked_at;

-- name: AgentCallbacksRevokedAt :one
SELECT revoked_at FROM agent_callback_revocations
 WHERE endpoint_id = sqlc.arg(endpoint_id)::text::uuid AND thread_id = sqlc.arg(thread_id)::text;

-- name: InsertAgentRunCallback :exec
-- ADR-20261030125501 §4: the thread and tool call a gregale_run run answers.
-- A retried invocation that found its run keeps the first link.
INSERT INTO agent_run_callbacks (run_id, account_id, endpoint_id, thread_id, tool_call_id, provider_call_id, issued_at)
VALUES (sqlc.arg(run_id)::text::uuid, sqlc.arg(account_id)::text::uuid, sqlc.arg(endpoint_id)::text::uuid,
  sqlc.arg(thread_id)::text, sqlc.arg(tool_call_id)::text, sqlc.narg(provider_call_id)::text, sqlc.arg(issued_at)::timestamptz)
ON CONFLICT (run_id) DO NOTHING;

-- name: ListUndeliveredAgentRunCallbacks :many
-- Finished gregale_run runs whose end apid has not yet published into their
-- thread. The mark is written after the publish, which is idempotent, so an
-- interrupted delivery is retried and lands once.
SELECT c.run_id::text AS run_id, c.account_id::text AS account_id, c.endpoint_id::text AS endpoint_id, c.thread_id,
  c.tool_call_id, c.provider_call_id, c.issued_at, r.state, r.outcome_code, r.outcome_message, o.result
 FROM agent_run_callbacks c JOIN agent_runs r ON r.id=c.run_id LEFT JOIN agent_run_outputs o ON o.run_id=c.run_id
 WHERE c.delivered_at IS NULL AND r.finished_at IS NOT NULL
 ORDER BY r.finished_at, c.run_id LIMIT sqlc.arg(max_rows)::integer;

-- name: MarkAgentRunCallbackDelivered :exec
UPDATE agent_run_callbacks SET delivered_at = now() WHERE run_id = sqlc.arg(run_id)::text::uuid AND delivered_at IS NULL;

-- name: LockAgentGitHubSubscriptionEndpoint :exec
-- ADR-20261030125501 §4: serializes gregale_github_subscribe calls of one
-- endpoint, so its live subscriptions stay within the plan's limit.
SELECT pg_advisory_xact_lock(hashtextextended('agent-github-subscriptions:' || sqlc.arg(endpoint_id)::text, 0));

-- name: AgentGitHubSubscriptionByCall :one
SELECT id::text AS id, repository, event_types, expires_at FROM agent_github_subscriptions
 WHERE endpoint_id = sqlc.arg(endpoint_id)::text::uuid AND thread_id = sqlc.arg(thread_id)::text
   AND tool_call_id = sqlc.arg(tool_call_id)::text;

-- name: CountLiveAgentGitHubSubscriptions :one
SELECT count(*)::integer FROM agent_github_subscriptions
 WHERE endpoint_id = sqlc.arg(endpoint_id)::text::uuid AND expires_at > sqlc.arg(now)::timestamptz;

-- name: InsertAgentGitHubSubscription :one
INSERT INTO agent_github_subscriptions (account_id, endpoint_id, thread_id, tool_call_id, repository, event_types, issued_at, expires_at)
VALUES (sqlc.arg(account_id)::text::uuid, sqlc.arg(endpoint_id)::text::uuid, sqlc.arg(thread_id)::text, sqlc.arg(tool_call_id)::text,
  sqlc.arg(repository)::text, sqlc.arg(event_types)::text[], sqlc.arg(issued_at)::timestamptz, sqlc.arg(expires_at)::timestamptz)
RETURNING id::text AS id;

-- name: ListLiveAgentGitHubSubscriptions :many
-- The live subscriptions of one account to one repository's event type.
SELECT id::text AS id, endpoint_id::text AS endpoint_id, thread_id, tool_call_id, issued_at
 FROM agent_github_subscriptions
 WHERE account_id = sqlc.arg(account_id)::text::uuid AND repository = sqlc.arg(repository)::text
   AND sqlc.arg(event_type)::text = ANY (event_types) AND expires_at > sqlc.arg(now)::timestamptz
 ORDER BY created_at, id LIMIT sqlc.arg(max_rows)::integer;

-- name: DeleteExpiredAgentGitHubSubscriptions :execrows
DELETE FROM agent_github_subscriptions WHERE id IN (
  SELECT id FROM agent_github_subscriptions WHERE expires_at < sqlc.arg(before)::timestamptz
   ORDER BY expires_at LIMIT sqlc.arg(max_rows)::integer);

-- name: AgentCallbackClock :one
-- ADR-20261030125501 §3: the clock RevokeAgentCallbacks records, read when a
-- callback URL is issued so issue and revocation compare on one clock.
SELECT clock_timestamp()::timestamptz AS now;

-- name: CountQueuedSandboxSessions :one
-- ADR-20261027090013 §7: queued sessions share AgentRunQueuedPerAccount with queued runs.
SELECT count(*)::integer FROM sandbox_sessions WHERE account_id=sqlc.arg(account_id)::text::uuid AND state='queued';

-- name: InsertSandboxSession :one
INSERT INTO sandbox_sessions (account_id,tenant_id,kind,preset,ram_mb,network_mode,idle_timeout_sec,expires_at,labels,workspace_snapshot_id,
  image_ref,image_job_id,template_id,browser,task_kind,task_url,task_max_chars,egress_allow,egress_deny,vcpu,volume_gb)
VALUES (sqlc.arg(account_id)::text::uuid,sqlc.narg(tenant_id)::text::uuid,sqlc.arg(kind)::text,sqlc.arg(preset)::text,
  sqlc.arg(ram_mb)::integer,sqlc.arg(network_mode)::text,sqlc.arg(idle_timeout_sec)::integer,sqlc.arg(expires_at)::timestamptz,
  sqlc.arg(labels)::jsonb,sqlc.narg(workspace_snapshot_id)::text::uuid,sqlc.narg(image_ref)::text,sqlc.narg(image_job_id)::text::uuid,
  sqlc.narg(template_id)::text::uuid,sqlc.arg(browser)::boolean,sqlc.narg(task_kind)::text,sqlc.narg(task_url)::text,
  sqlc.narg(task_max_chars)::integer,sqlc.narg(egress_allow)::text[],sqlc.narg(egress_deny)::text[],
  sqlc.narg(vcpu)::smallint,sqlc.narg(volume_gb)::integer)
RETURNING id::text AS id;

-- name: CountPersistentSandboxSessions :one
-- An account's persistent sessions that have not ended, paused or not
-- (SandboxPersistentMax, ADR-20261122132055 §6).
SELECT count(*)::bigint FROM sandbox_sessions
 WHERE account_id=sqlc.arg(account_id)::text::uuid AND kind='persistent'
 AND state IN ('queued','starting','ready','frozen','pausing','paused','resuming');

-- name: GetSandboxSession :one
SELECT id::text AS id,account_id::text AS account_id,coalesce(tenant_id::text, '')::text AS tenant_id,kind,preset,ram_mb,network_mode,state,
  idle_timeout_sec,expires_at,labels,outcome_code,outcome_message,close_requested_at,last_activity_at,created_at,updated_at,closed_at,
  coalesce(node_id, '')::text AS node_id,coalesce(instance_id, '')::text AS instance_id,
  coalesce(workspace_snapshot_id::text, '')::text AS workspace_snapshot_id, coalesce(image_ref, '')::text AS image_ref, browser,
  coalesce(egress_allow, '{}')::text[] AS egress_allow, coalesce(egress_deny, '{}')::text[] AS egress_deny,
  coalesce(vcpu, 0)::integer AS vcpu, coalesce(volume_gb, 0)::integer AS volume_gb, pause_requested_at, resume_requested_at,
  paused_at, coalesce(durability, '')::text AS durability, resumed_cold, coalesce(forked_from::text, '')::text AS forked_from
 FROM sandbox_sessions WHERE id=sqlc.arg(session_id)::text::uuid AND account_id=sqlc.arg(account_id)::text::uuid;

-- name: ListSandboxSessions :many
-- Newest first; a page continues before the (created_at, id) of the last row.
SELECT id::text AS id,account_id::text AS account_id,coalesce(tenant_id::text, '')::text AS tenant_id,kind,preset,ram_mb,network_mode,state,
  idle_timeout_sec,expires_at,labels,outcome_code,outcome_message,close_requested_at,last_activity_at,created_at,updated_at,closed_at,
  coalesce(node_id, '')::text AS node_id,coalesce(instance_id, '')::text AS instance_id,
  coalesce(workspace_snapshot_id::text, '')::text AS workspace_snapshot_id, coalesce(image_ref, '')::text AS image_ref, browser,
  coalesce(egress_allow, '{}')::text[] AS egress_allow, coalesce(egress_deny, '{}')::text[] AS egress_deny,
  coalesce(vcpu, 0)::integer AS vcpu, coalesce(volume_gb, 0)::integer AS volume_gb, pause_requested_at, resume_requested_at,
  paused_at, coalesce(durability, '')::text AS durability, resumed_cold, coalesce(forked_from::text, '')::text AS forked_from
 FROM sandbox_sessions
 WHERE account_id=sqlc.arg(account_id)::text::uuid
 AND (sqlc.narg(state)::text IS NULL OR state=sqlc.narg(state)::text)
 AND (sqlc.narg(before_created_at)::timestamptz IS NULL OR (created_at, id) < (sqlc.narg(before_created_at)::timestamptz, sqlc.narg(before_id)::text::uuid))
 ORDER BY created_at DESC, id DESC LIMIT sqlc.arg(max_rows)::integer;

-- name: UpdateSandboxSessionTimeouts :execrows
-- A live session's idle timeout and end; a terminal one is unchanged.
UPDATE sandbox_sessions SET idle_timeout_sec=sqlc.arg(idle_timeout_sec)::integer, expires_at=sqlc.arg(expires_at)::timestamptz, updated_at=now()
 WHERE id=sqlc.arg(session_id)::text::uuid AND account_id=sqlc.arg(account_id)::text::uuid
 AND state IN ('queued','starting','ready','frozen');

-- name: CloseQueuedSandboxSession :execrows
-- No VM exists yet, so apid closes a queued session itself.
UPDATE sandbox_sessions SET state='closed', outcome_code='closed', closed_at=now(), updated_at=now()
 WHERE id=sqlc.arg(session_id)::text::uuid AND account_id=sqlc.arg(account_id)::text::uuid AND state='queued';

-- name: RequestSandboxSessionClose :execrows
-- A claimed session is agentd's: apid records the request, and agentd tears
-- the VM down and closes it.
UPDATE sandbox_sessions SET close_requested_at=coalesce(close_requested_at, now()), updated_at=now()
 WHERE id=sqlc.arg(session_id)::text::uuid AND account_id=sqlc.arg(account_id)::text::uuid
 AND state IN ('starting','ready','frozen','pausing','paused','resuming');

-- name: RequestSandboxSessionPause :execrows
-- A running persistent session's pause is agentd's: apid records the
-- request (ADR-20261122132055 §1). A pause cancels a pending resume.
UPDATE sandbox_sessions SET pause_requested_at=coalesce(pause_requested_at, now()), resume_requested_at=NULL, updated_at=now()
 WHERE id=sqlc.arg(session_id)::text::uuid AND account_id=sqlc.arg(account_id)::text::uuid
 AND kind='persistent' AND state IN ('ready','frozen') AND close_requested_at IS NULL;

-- name: RequestSandboxSessionResume :execrows
-- A paused (or pausing) persistent session's resume is agentd's too.
UPDATE sandbox_sessions SET resume_requested_at=coalesce(resume_requested_at, now()), updated_at=now()
 WHERE id=sqlc.arg(session_id)::text::uuid AND account_id=sqlc.arg(account_id)::text::uuid
 AND kind='persistent' AND state IN ('pausing','paused') AND close_requested_at IS NULL;

-- name: ListClaimableSandboxSessions :many
-- Oldest first; rows another replica holds are skipped, not waited on.
-- A session from a seller image waits until the image-run runner job has
-- its image ready, and one from a template until it has a finished build
-- (ADR-20261027090013).
-- A paused persistent session asked to resume is claimable too
-- (ADR-20261122132055 §1).
SELECT sb.id::text AS id, sb.account_id::text AS account_id FROM sandbox_sessions sb
 WHERE (sb.state='queued' AND sb.expires_at > now()
 AND (sb.image_ref IS NULL OR EXISTS (SELECT 1 FROM jobs j WHERE j.id=sb.image_job_id AND j.account_id=sb.account_id
   AND j.status <> 'deleted' AND j.image_materialization_status='ready' AND j.image_storage_key IS NOT NULL)
   OR EXISTS (SELECT 1 FROM sandbox_templates t JOIN deployments d ON d.app_id=t.app_id
   WHERE t.id=sb.template_id AND t.account_id=sb.account_id AND d.status='live' AND d.rootfs_key <> ''))
 AND (sb.forked_from IS NULL OR EXISTS (SELECT 1 FROM sandbox_session_captures c
   WHERE c.id=sb.fork_capture_id AND c.uploaded_at IS NOT NULL)))
 OR (sb.state='paused' AND sb.resume_requested_at IS NOT NULL AND sb.close_requested_at IS NULL AND sb.expires_at > now()
   AND (sb.resume_not_before IS NULL OR sb.resume_not_before <= now()))
 ORDER BY sb.created_at, sb.id LIMIT sqlc.arg(max_rows)::integer FOR UPDATE OF sb SKIP LOCKED;

-- name: StartSandboxSession :execrows
-- A queued session starts; a paused one asked to resume resumes.
UPDATE sandbox_sessions SET state=CASE WHEN state='paused' THEN 'resuming' ELSE 'starting' END,
  resume_requested_at=CASE WHEN state='paused' THEN NULL ELSE resume_requested_at END, resume_not_before=NULL,
  lease_token=sqlc.arg(lease_token)::text, lease_owner=sqlc.arg(lease_owner)::text,
  lease_expires_at=now()+make_interval(secs=>sqlc.arg(ttl_seconds)::double precision), updated_at=now()
 WHERE id=sqlc.arg(session_id)::text::uuid
 AND (state='queued' OR (state='paused' AND resume_requested_at IS NOT NULL AND close_requested_at IS NULL AND expires_at > now()
   AND (resume_not_before IS NULL OR resume_not_before <= now())));

-- name: GetSandboxSessionClaim :one
-- A session that starts from a persisted workspace carries its archive's
-- key; workspace_missing marks one whose snapshot is gone or deleted.
SELECT sb.id::text AS id, sb.account_id::text AS account_id, a.plan, sb.ram_mb, sb.network_mode, sb.idle_timeout_sec, sb.expires_at,
  (sb.close_requested_at IS NOT NULL)::boolean AS close_requested,
  coalesce(ws.storage_key, '')::text AS workspace_key,
  (sb.workspace_snapshot_id IS NOT NULL AND (ws.id IS NULL OR ws.state <> 'ready' OR ws.delete_requested_at IS NOT NULL))::boolean AS workspace_missing,
  coalesce(CASE WHEN j.status <> 'deleted' AND j.image_materialization_status='ready' THEN j.image_storage_key END,
    tpl.rootfs_key, '')::text AS image_key,
  (sb.image_ref IS NOT NULL)::boolean AS from_image,
  coalesce(st.start_cmd, '')::text AS start_cmd, coalesce(st.ready_cmd, '')::text AS ready_cmd,
  coalesce(st.ready_timeout_s, 0)::integer AS ready_timeout_s, sb.browser,
  coalesce(sb.task_kind, '')::text AS task_kind, coalesce(sb.task_url, '')::text AS task_url,
  coalesce(sb.task_max_chars, 0)::integer AS task_max_chars,
  coalesce(sb.egress_allow, '{}')::text[] AS egress_allow, coalesce(sb.egress_deny, '{}')::text[] AS egress_deny,
  sb.state, sb.kind, coalesce(sb.vcpu, 0)::integer AS vcpu,
  coalesce(cap.id::text, '')::text AS capture_id, coalesce(cap.storage_key, '')::text AS capture_storage_key,
  coalesce(cap.vmstate_storage_key, '')::text AS capture_vmstate_storage_key, coalesce(cap.fc_version, '')::text AS capture_fc_version,
  coalesce(cap.mem_bytes, 0)::bigint AS capture_mem_bytes, coalesce(cap.node_id, '')::text AS capture_node_id,
  coalesce(sb.durability, '')::text AS durability,
  coalesce(sb.volume_gb, 0)::integer AS volume_gb, coalesce(vol.latest_backup_id::text, '')::text AS volume_backup_id,
  coalesce(vol.node_id, '')::text AS volume_node_id,
  coalesce((SELECT b.id::text FROM sandbox_volume_backups b WHERE b.session_id=sb.id AND b.state='stored'
    ORDER BY b.created_at DESC, b.id DESC LIMIT 1), '')::text AS volume_stored_backup_id,
  coalesce(sb.fork_capture_id::text, '')::text AS fork_capture_id,
  coalesce(cap.volume_backup_id::text, '')::text AS capture_volume_backup_id,
  coalesce(cap.session_id::text, '')::text AS capture_session_id
 FROM sandbox_sessions sb JOIN accounts a ON a.id=sb.account_id
 LEFT JOIN sandbox_session_captures cap ON (sb.fork_capture_id IS NOT NULL AND cap.id=sb.fork_capture_id)
   OR (sb.fork_capture_id IS NULL AND cap.session_id=sb.id AND cap.state='live' AND cap.purpose='pause')
 LEFT JOIN sandbox_volumes vol ON vol.session_id=sb.id
 LEFT JOIN sandbox_templates st ON st.id=sb.template_id AND st.account_id=sb.account_id
 LEFT JOIN sandbox_workspace_snapshots ws ON ws.id=sb.workspace_snapshot_id AND ws.account_id=sb.account_id
 LEFT JOIN jobs j ON j.id=sb.image_job_id AND j.account_id=sb.account_id
 LEFT JOIN LATERAL (SELECT d.rootfs_key FROM sandbox_templates t JOIN deployments d ON d.app_id=t.app_id
   WHERE t.id=sb.template_id AND t.account_id=sb.account_id AND d.status='live' AND d.rootfs_key <> ''
   ORDER BY d.created_at DESC, d.id DESC LIMIT 1) tpl ON true
 WHERE sb.id=sqlc.arg(session_id)::text::uuid;

-- name: MarkSandboxSessionPlaced :execrows
-- schedd placed the session (ADR-20261117190000): record the node before it
-- boots the VM, so secretd grants that node the session's image and
-- snapshot. node_id stays unset until the VM is up.
UPDATE sandbox_sessions SET placed_node_id=sqlc.arg(node_id)::text, updated_at=now()
 WHERE id=sqlc.arg(session_id)::text::uuid AND lease_token=sqlc.arg(lease_token)::text AND state IN ('starting','resuming');

-- name: MarkSandboxSessionReady :execrows
-- The VM is up: record where it runs, so a read mints a token that reaches it.
UPDATE sandbox_sessions SET state='ready', node_id=sqlc.arg(node_id)::text, instance_id=sqlc.arg(instance_id)::text,
  fork_capture_id=NULL, last_activity_at=now(), updated_at=now()
 WHERE id=sqlc.arg(session_id)::text::uuid AND lease_token=sqlc.arg(lease_token)::text AND state='starting';

-- name: RenewSandboxSessionLease :one
-- Renewing also records the last call agentd has seen, so last_activity_at
-- costs no write per call. It returns what apid may have changed.
UPDATE sandbox_sessions SET lease_expires_at=now()+make_interval(secs=>sqlc.arg(ttl_seconds)::double precision),
  last_activity_at=greatest(last_activity_at, sqlc.arg(last_activity_at)::timestamptz), updated_at=now()
 WHERE id=sqlc.arg(session_id)::text::uuid AND lease_token=sqlc.arg(lease_token)::text
 AND state IN ('starting','ready','frozen','pausing','resuming')
 RETURNING (close_requested_at IS NOT NULL)::boolean AS close_requested, idle_timeout_sec, expires_at,
  (pause_requested_at IS NOT NULL)::boolean AS pause_requested, (fork_requested_at IS NOT NULL)::boolean AS fork_requested;

-- name: MarkSandboxSessionResumed :execrows
-- A resumed session is ready again (ADR-20261122132055 §2): its capture is
-- used (a capture restores once), the next automatic pause is run_seconds
-- away, and resumed_cold says whether the capture could not be restored.
WITH session AS (
  SELECT id FROM sandbox_sessions
   WHERE id=sqlc.arg(session_id)::text::uuid AND lease_token=sqlc.arg(lease_token)::text AND state='resuming'
), used AS (
  UPDATE sandbox_session_captures SET state='consumed', consumed_at=now()
   WHERE session_id IN (SELECT id FROM session) AND state='live'
)
UPDATE sandbox_sessions sb SET state='ready', node_id=sqlc.arg(node_id)::text, instance_id=sqlc.arg(instance_id)::text,
  last_activity_at=now(), paused_at=NULL, durability=NULL, resumed_cold=sqlc.arg(resumed_cold)::boolean,
  expires_at=now()+make_interval(secs=>sqlc.arg(run_seconds)::double precision), updated_at=now()
 WHERE sb.id IN (SELECT id FROM session);

-- name: BeginSandboxSessionPause :execrows
-- agentd pauses a running persistent session (ADR-20261122132055 §1). A
-- resume asked for before this pause began belongs to an earlier one.
UPDATE sandbox_sessions SET state='pausing', resume_requested_at=NULL, updated_at=now()
 WHERE id=sqlc.arg(session_id)::text::uuid AND lease_token=sqlc.arg(lease_token)::text
 AND kind='persistent' AND state IN ('ready','frozen');

-- name: CancelSandboxSessionPause :execrows
-- vmmd refused the pause and the VM runs on, frozen if it still is; a
-- resume asked for meanwhile is answered by the running VM.
UPDATE sandbox_sessions SET state=CASE WHEN sqlc.arg(frozen)::boolean THEN 'frozen' ELSE 'ready' END,
  resume_requested_at=NULL, updated_at=now()
 WHERE id=sqlc.arg(session_id)::text::uuid AND lease_token=sqlc.arg(lease_token)::text AND state='pausing';

-- name: ReturnSandboxSessionResume :execrows
-- A resume that never booted goes back to paused with its capture. retry
-- keeps the request, so a claim tries again after retry_seconds; a refused
-- resume drops it until apid asks again.
UPDATE sandbox_sessions SET state='paused',
  resume_requested_at=CASE WHEN sqlc.arg(retry)::boolean THEN coalesce(resume_requested_at, now()) ELSE NULL END,
  resume_not_before=CASE WHEN sqlc.arg(retry)::boolean THEN now()+make_interval(secs=>sqlc.arg(retry_seconds)::double precision) END,
  lease_token=NULL, lease_owner=NULL, lease_expires_at=NULL, updated_at=now()
 WHERE id=sqlc.arg(session_id)::text::uuid AND lease_token=sqlc.arg(lease_token)::text AND state='resuming';

-- name: MarkSandboxSessionPaused :execrows
-- The VM is captured and gone: the session keeps its capture, holds no lease
-- and no VM, and expires retention_seconds from now unless it resumes.
WITH session AS (
  SELECT id, account_id FROM sandbox_sessions
   WHERE id=sqlc.arg(session_id)::text::uuid AND lease_token=sqlc.arg(lease_token)::text AND state='pausing'
), capture AS (
  INSERT INTO sandbox_session_captures (id, session_id, account_id, node_id, storage_key, vmstate_storage_key, fc_version,
    mem_bytes, vmstate_bytes, has_backing, volume_backup_id)
  SELECT sqlc.arg(capture_id)::text::uuid, id, account_id, sqlc.arg(node_id)::text, sqlc.arg(storage_key)::text,
    sqlc.arg(vmstate_storage_key)::text, sqlc.arg(fc_version)::text, sqlc.arg(mem_bytes)::bigint, sqlc.arg(vmstate_bytes)::bigint,
    sqlc.arg(has_backing)::boolean, NULLIF(sqlc.arg(volume_backup_id)::text, '')::uuid
  FROM session
  RETURNING session_id
), forks AS (
  UPDATE sandbox_sessions f SET fork_capture_id=sqlc.arg(capture_id)::text::uuid, updated_at=now()
   WHERE f.forked_from IN (SELECT session_id FROM capture) AND f.fork_capture_id IS NULL AND f.state='queued'
)
UPDATE sandbox_sessions sb SET state='paused', paused_at=now(), pause_requested_at=NULL, fork_requested_at=NULL, durability='node',
  node_id=NULL, instance_id=NULL, lease_token=NULL, lease_owner=NULL, lease_expires_at=NULL,
  expires_at=now()+make_interval(secs=>sqlc.arg(retention_seconds)::double precision), updated_at=now()
 WHERE sb.id IN (SELECT session_id FROM capture);

-- name: SetSandboxSessionFrozen :execrows
-- A pause thaws a frozen VM before it captures it; the session stays
-- pausing (ADR-20261122132055 §2).
UPDATE sandbox_sessions SET state=CASE WHEN state='pausing' THEN 'pausing'
    WHEN sqlc.arg(frozen)::boolean THEN 'frozen' ELSE 'ready' END, updated_at=now()
 WHERE id=sqlc.arg(session_id)::text::uuid AND lease_token=sqlc.arg(lease_token)::text AND state IN ('ready','frozen','pausing');

-- name: RecordSandboxSessionUsage :execrows
-- ADR-20261027090013 step 11: vmmd's cumulative usage of the session VM,
-- while the lease is held and the session has a VM. A record never replaces
-- a later one of the same VM run (a reordered or replayed frame), nor a final
-- one of it. A persistent session's resume runs a new VM under a new lease
-- whose counters start at zero: its first record keeps the totals so far as
-- the base, and every record of the run adds the base, so usage sums across
-- pauses (ADR-20261122132055).
WITH prev AS (
  SELECT p.samples, p.expected_samples, p.measured_ms, p.paused_ms, p.frozen_ms, p.private_mb_ms, p.unpaused_private_mb_ms,
         p.billable_mb_ms, p.peak_private_mb, p.base_samples, p.base_expected_samples, p.base_measured_ms, p.base_paused_ms,
         p.base_frozen_ms, p.base_private_mb_ms, p.base_unpaused_private_mb_ms, p.base_billable_mb_ms,
         (p.lease_token IS DISTINCT FROM sqlc.arg(lease_token)::text AND (p.final OR p.lease_token IS NOT NULL)) AS new_run
    FROM sandbox_session_usage p WHERE p.sandbox_id=sqlc.arg(session_id)::text::uuid
), base AS (
  SELECT CASE WHEN new_run THEN samples ELSE base_samples END AS samples,
         CASE WHEN new_run THEN expected_samples ELSE base_expected_samples END AS expected_samples,
         CASE WHEN new_run THEN measured_ms ELSE base_measured_ms END AS measured_ms,
         CASE WHEN new_run THEN paused_ms ELSE base_paused_ms END AS paused_ms,
         CASE WHEN new_run THEN frozen_ms ELSE base_frozen_ms END AS frozen_ms,
         CASE WHEN new_run THEN private_mb_ms ELSE base_private_mb_ms END AS private_mb_ms,
         CASE WHEN new_run THEN unpaused_private_mb_ms ELSE base_unpaused_private_mb_ms END AS unpaused_private_mb_ms,
         CASE WHEN new_run THEN billable_mb_ms ELSE base_billable_mb_ms END AS billable_mb_ms,
         peak_private_mb
    FROM prev
)
INSERT INTO sandbox_session_usage AS u (sandbox_id, account_id, method, samples, expected_samples, measured_ms, paused_ms,
    frozen_ms, private_mb_ms, unpaused_private_mb_ms, billable_mb_ms, peak_private_mb, cold_boot, final, lease_token,
    base_samples, base_expected_samples, base_measured_ms, base_paused_ms, base_frozen_ms, base_private_mb_ms,
    base_unpaused_private_mb_ms, base_billable_mb_ms)
SELECT s.id, s.account_id, sqlc.arg(method)::smallint,
       coalesce(b.samples, 0)+sqlc.arg(samples)::integer, coalesce(b.expected_samples, 0)+sqlc.arg(expected_samples)::integer,
       coalesce(b.measured_ms, 0)+sqlc.arg(measured_ms)::bigint, coalesce(b.paused_ms, 0)+sqlc.arg(paused_ms)::bigint,
       coalesce(b.frozen_ms, 0)+sqlc.arg(frozen_ms)::bigint, coalesce(b.private_mb_ms, 0)+sqlc.arg(private_mb_ms)::bigint,
       coalesce(b.unpaused_private_mb_ms, 0)+sqlc.arg(unpaused_private_mb_ms)::bigint,
       coalesce(b.billable_mb_ms, 0)+sqlc.arg(billable_mb_ms)::bigint,
       greatest(coalesce(b.peak_private_mb, 0), sqlc.arg(peak_private_mb)::bigint),
       sqlc.arg(cold_boot)::boolean, sqlc.arg(final)::boolean, s.lease_token,
       coalesce(b.samples, 0), coalesce(b.expected_samples, 0), coalesce(b.measured_ms, 0), coalesce(b.paused_ms, 0),
       coalesce(b.frozen_ms, 0), coalesce(b.private_mb_ms, 0), coalesce(b.unpaused_private_mb_ms, 0), coalesce(b.billable_mb_ms, 0)
  FROM sandbox_sessions s LEFT JOIN base b ON true
 WHERE s.id=sqlc.arg(session_id)::text::uuid AND s.lease_token=sqlc.arg(lease_token)::text AND s.state IN ('ready','frozen','pausing')
ON CONFLICT (sandbox_id) DO UPDATE SET method=excluded.method, samples=excluded.samples, expected_samples=excluded.expected_samples,
       measured_ms=excluded.measured_ms, paused_ms=excluded.paused_ms, frozen_ms=excluded.frozen_ms,
       private_mb_ms=excluded.private_mb_ms, unpaused_private_mb_ms=excluded.unpaused_private_mb_ms,
       billable_mb_ms=excluded.billable_mb_ms, peak_private_mb=excluded.peak_private_mb, cold_boot=excluded.cold_boot,
       final=excluded.final, lease_token=excluded.lease_token, base_samples=excluded.base_samples,
       base_expected_samples=excluded.base_expected_samples, base_measured_ms=excluded.base_measured_ms,
       base_paused_ms=excluded.base_paused_ms, base_frozen_ms=excluded.base_frozen_ms,
       base_private_mb_ms=excluded.base_private_mb_ms, base_unpaused_private_mb_ms=excluded.base_unpaused_private_mb_ms,
       base_billable_mb_ms=excluded.base_billable_mb_ms, updated_at=now()
 WHERE (u.lease_token IS DISTINCT FROM excluded.lease_token AND (u.final OR u.lease_token IS NOT NULL))
    OR (NOT u.final AND (u.lease_token IS NULL OR u.lease_token=excluded.lease_token) AND excluded.measured_ms >= u.measured_ms);

-- name: GetSandboxSessionUsage :one
SELECT method, samples, expected_samples, measured_ms, paused_ms, frozen_ms, private_mb_ms, unpaused_private_mb_ms,
       billable_mb_ms, peak_private_mb, cold_boot, final
  FROM sandbox_session_usage WHERE sandbox_id=sqlc.arg(session_id)::text::uuid AND account_id=sqlc.arg(account_id)::text::uuid;

-- name: RequeueSandboxSession :execrows
-- Only a starting session, which has no VM, goes back to the queue.
UPDATE sandbox_sessions SET state='queued', lease_token=NULL, lease_expires_at=NULL, lease_owner=NULL, updated_at=now()
 WHERE id=sqlc.arg(session_id)::text::uuid AND lease_token=sqlc.arg(lease_token)::text AND state='starting';

-- name: FinishSandboxSession :execrows
-- A session that ends, from resuming too, never restores its capture: it
-- is used, so the sweep deletes it.
WITH used AS (
  UPDATE sandbox_session_captures SET state='consumed', consumed_at=now()
   WHERE session_id=sqlc.arg(session_id)::text::uuid AND state='live' AND sqlc.arg(state)::text IN ('closed','expired','failed')
   AND EXISTS (SELECT 1 FROM sandbox_sessions WHERE id=sqlc.arg(session_id)::text::uuid AND lease_token=sqlc.arg(lease_token)::text
     AND state IN ('starting','ready','frozen','pausing','resuming'))
)
UPDATE sandbox_sessions SET state=sqlc.arg(state)::text, outcome_code=sqlc.arg(outcome_code)::text,
  outcome_message=sqlc.narg(outcome_message)::text, lease_token=NULL, lease_expires_at=NULL, closed_at=now(), updated_at=now()
 WHERE id=sqlc.arg(session_id)::text::uuid AND lease_token=sqlc.arg(lease_token)::text
 AND state IN ('starting','ready','frozen','pausing','resuming')
 AND sqlc.arg(state)::text IN ('closed','expired','failed');

-- name: RequeueExpiredStartingSandboxSessions :execrows
UPDATE sandbox_sessions SET state='queued', lease_token=NULL, lease_expires_at=NULL, lease_owner=NULL, updated_at=now()
 WHERE state='starting' AND lease_expires_at < now();

-- name: PauseExpiredPersistentSandboxSessions :execrows
-- A persistent session with a volume whose replica stopped renewing lost
-- its VM, not its files: it goes back to paused without a capture and
-- resumes cold from its volume (ADR-20261122132055 §4), kept for its plan's
-- paused retention. Run before FailExpiredLiveSandboxSessions.
UPDATE sandbox_sessions sb SET state='paused', paused_at=now(), pause_requested_at=NULL, resume_requested_at=NULL,
  durability=NULL, node_id=NULL, instance_id=NULL, lease_token=NULL, lease_owner=NULL, lease_expires_at=NULL,
  expires_at=now()+make_interval(days=>(sqlc.arg(retention_days)::integer[])[array_position(sqlc.arg(plans)::text[], a.plan)]),
  updated_at=now()
  FROM accounts a
 WHERE a.id=sb.account_id
   AND coalesce((sqlc.arg(retention_days)::integer[])[array_position(sqlc.arg(plans)::text[], a.plan)], 0) > 0
   AND sb.kind='persistent' AND sb.state IN ('ready','frozen','pausing') AND sb.lease_expires_at < now()
   AND EXISTS (SELECT 1 FROM sandbox_volumes v WHERE v.session_id=sb.id);

-- name: PauseLostSandboxSession :execrows
-- A held persistent session with a volume whose VM ended unexpectedly goes
-- back to paused without a capture; it resumes cold from its volume
-- (ADR-20261122132055 §4).
UPDATE sandbox_sessions sb SET state='paused', paused_at=now(), pause_requested_at=NULL, resume_requested_at=NULL,
  durability=NULL, node_id=NULL, instance_id=NULL, lease_token=NULL, lease_owner=NULL, lease_expires_at=NULL,
  expires_at=now()+make_interval(secs=>sqlc.arg(retention_seconds)::double precision), updated_at=now()
 WHERE sb.id=sqlc.arg(session_id)::text::uuid AND sb.lease_token=sqlc.arg(lease_token)::text
   AND sb.kind='persistent' AND sb.state IN ('ready','frozen','pausing')
   AND EXISTS (SELECT 1 FROM sandbox_volumes v WHERE v.session_id=sb.id);

-- name: FailExpiredLiveSandboxSessions :execrows
-- A live session whose replica stopped renewing has lost its VM with the
-- replica's stream; it is never booted again.
UPDATE sandbox_sessions SET state='failed', outcome_code='lease_lost',
  outcome_message='agentd lost the session; its VM ended with it', lease_token=NULL, lease_expires_at=NULL, closed_at=now(), updated_at=now()
 WHERE state IN ('ready','frozen','pausing') AND lease_expires_at < now();

-- name: ReturnExpiredResumingSandboxSessions :execrows
-- A resume whose replica stopped renewing ended with its stream before the
-- capture was used: the session is paused again and resumes on the next
-- claim (ADR-20261122132055 §2).
UPDATE sandbox_sessions SET state='paused', resume_requested_at=coalesce(resume_requested_at, now()),
  lease_token=NULL, lease_owner=NULL, lease_expires_at=NULL, updated_at=now()
 WHERE state='resuming' AND lease_expires_at < now();

-- name: EndPausedSandboxSessions :many
-- A paused session ends without a VM: closed when apid asked, expired past
-- its retention (ADR-20261122132055 §1). Its live capture is left for the
-- capture sweep.
WITH ended AS (
  UPDATE sandbox_sessions SET state=CASE WHEN close_requested_at IS NOT NULL THEN 'closed' ELSE 'expired' END,
    outcome_code=CASE WHEN close_requested_at IS NOT NULL THEN 'closed' ELSE 'retention' END,
    outcome_message=CASE WHEN close_requested_at IS NOT NULL THEN NULL ELSE 'the session stayed paused past its retention' END,
    closed_at=now(), updated_at=now()
   WHERE state='paused' AND (close_requested_at IS NOT NULL OR expires_at <= now())
  RETURNING id
), used AS (
  UPDATE sandbox_session_captures SET state='consumed', consumed_at=now()
   WHERE session_id IN (SELECT id FROM ended) AND state='live'
)
SELECT id::text AS id FROM ended;

-- name: ListConsumedSandboxSessionCaptures :many
-- Captures a resume used or a session's end left: the sweep deletes their
-- objects once their node's upload is over, then the row. Oldest capture
-- first: those are past any upload. A fork capture waits for the forks that
-- still restore it.
SELECT cap.id::text AS id, cap.storage_key, cap.vmstate_storage_key, cap.has_backing, cap.created_at FROM sandbox_session_captures cap
 WHERE cap.state='consumed' AND NOT EXISTS (SELECT 1 FROM sandbox_sessions f
   WHERE f.fork_capture_id=cap.id AND f.state IN ('queued','starting'))
 ORDER BY cap.checked_at NULLS FIRST, cap.created_at, cap.id LIMIT sqlc.arg(max_rows)::integer;

-- name: TouchSandboxSessionCaptures :exec
-- A sweep found these captures not yet uploaded: the next sweeps take
-- others first.
UPDATE sandbox_session_captures SET checked_at=now() WHERE id = ANY(sqlc.arg(ids)::uuid[]);

-- name: RecordAbandonedSandboxSessionCapture :exec
-- A pause agentd never saw answered: vmmd may have written a capture under
-- its key, so a consumed row lets the sweep delete it.
INSERT INTO sandbox_session_captures (id, session_id, account_id, node_id, storage_key, vmstate_storage_key, fc_version,
  mem_bytes, has_backing, state, consumed_at)
SELECT sqlc.arg(capture_id)::text::uuid, sb.id, sb.account_id, sqlc.arg(node_id)::text, sqlc.arg(storage_key)::text,
  sqlc.arg(vmstate_storage_key)::text, '', 0, true, 'consumed', now()
  FROM sandbox_sessions sb WHERE sb.id=sqlc.arg(session_id)::text::uuid
ON CONFLICT (id) DO NOTHING;

-- name: ListNodeOnlySandboxCaptures :many
-- Live captures of paused sessions the shared store may not hold yet
-- (durability node): agentd checks each and marks it remote once it does
-- (ADR-20261122132055 §2).
SELECT cap.id::text AS id, cap.session_id::text AS session_id, cap.account_id::text AS account_id, cap.storage_key,
  cap.vmstate_storage_key, cap.has_backing, coalesce(cap.volume_backup_id::text, '')::text AS volume_backup_id,
  (vol.session_id IS NOT NULL)::boolean AS has_volume
  FROM sandbox_session_captures cap JOIN sandbox_sessions sb ON sb.id=cap.session_id
  LEFT JOIN sandbox_volumes vol ON vol.session_id=cap.session_id
 WHERE cap.state='live' AND sb.state='paused' AND sb.durability='node'
 ORDER BY cap.checked_at NULLS FIRST, cap.created_at, cap.id LIMIT sqlc.arg(max_rows)::integer;

-- name: MarkSandboxCaptureRemote :execrows
WITH uploaded AS (
  UPDATE sandbox_session_captures SET uploaded_at=coalesce(uploaded_at, now())
   WHERE id=sqlc.arg(capture_id)::text::uuid AND session_id=sqlc.arg(session_id)::text::uuid AND state='live'
  RETURNING id
)
UPDATE sandbox_sessions sb SET durability='remote', updated_at=now()
 WHERE sb.id=sqlc.arg(session_id)::text::uuid AND sb.state='paused' AND sb.durability='node'
 AND EXISTS (SELECT 1 FROM uploaded);

-- name: MarkSandboxSessionVolumeNode :one
-- ADR-20261122132055 §4: the session's VM booted on node_id with its volume
-- at size_gb, so that node holds the newest copy; it returns the node that
-- held it before, whose copy is now stale.
WITH prev AS (
  SELECT node_id FROM sandbox_volumes WHERE session_id=sqlc.arg(session_id)::text::uuid
)
INSERT INTO sandbox_volumes AS v (session_id, account_id, node_id, size_gb)
SELECT sb.id, sb.account_id, sqlc.arg(node_id)::text, sqlc.arg(size_gb)::integer FROM sandbox_sessions sb
 WHERE sb.id=sqlc.arg(session_id)::text::uuid AND sb.lease_token=sqlc.arg(lease_token)::text
ON CONFLICT (session_id) DO UPDATE SET node_id=excluded.node_id, size_gb=excluded.size_gb, updated_at=now()
RETURNING coalesce((SELECT node_id FROM prev), '')::text AS previous_node_id;

-- name: RecordSandboxVolumeBackup :one
-- One backup vmmd reported for a session agentd holds; it becomes the
-- session's newest. A replayed report changes nothing.
WITH backup AS (
  INSERT INTO sandbox_volume_backups (id, session_id, account_id, node_id, reason, state, size_bytes, data_chunks, uploaded_bytes)
  SELECT sqlc.arg(backup_id)::text::uuid, sb.id, sb.account_id, sqlc.arg(node_id)::text, sqlc.arg(reason)::text,
    sqlc.arg(state)::text, sqlc.arg(size_bytes)::bigint, sqlc.arg(data_chunks)::integer, sqlc.arg(uploaded_bytes)::bigint
  FROM sandbox_sessions sb WHERE sb.id=sqlc.arg(session_id)::text::uuid AND sb.lease_token=sqlc.arg(lease_token)::text
  ON CONFLICT (id) DO NOTHING
  RETURNING session_id, id
), latest AS (
  UPDATE sandbox_volumes v SET latest_backup_id=b.id, updated_at=now() FROM backup b WHERE v.session_id=b.session_id
  RETURNING v.session_id
)
SELECT count(*)::bigint AS recorded FROM backup;

-- name: MarkSandboxVolumeBackupStored :execrows
UPDATE sandbox_volume_backups SET state='stored' WHERE id=sqlc.arg(backup_id)::text::uuid AND state='uploading';

-- name: ListSupersededSandboxVolumeBackups :many
-- Backups older than their session's newest that no live capture names: the
-- sweep deletes their manifests. A pause's upload still running is left
-- until vmmd would have given it up (upload_window_seconds, from
-- api.SandboxCaptureUploadSec).
SELECT b.id::text AS id, b.session_id::text AS session_id, b.account_id::text AS account_id
  FROM sandbox_volume_backups b
  JOIN sandbox_volumes v ON v.session_id=b.session_id
  JOIN sandbox_volume_backups l ON l.id=v.latest_backup_id
 WHERE b.state <> 'deleted' AND b.id <> l.id AND b.created_at < l.created_at
   AND (b.state='stored' OR b.created_at < now() - make_interval(secs=>sqlc.arg(upload_window_seconds)::double precision))
   AND NOT EXISTS (SELECT 1 FROM sandbox_session_captures c WHERE c.volume_backup_id=b.id
     AND (c.state='live' OR EXISTS (SELECT 1 FROM sandbox_sessions f WHERE f.fork_capture_id=c.id AND f.state IN ('queued','starting'))))
 ORDER BY b.created_at, b.id LIMIT sqlc.arg(max_rows)::integer;

-- name: ListEndedSandboxVolumes :many
-- Volumes of sessions that ended: the sweep deletes the node's copy and
-- every backup, then the row.
SELECT v.session_id::text AS session_id, v.account_id::text AS account_id, v.node_id
  FROM sandbox_volumes v JOIN sandbox_sessions sb ON sb.id=v.session_id
 WHERE sb.state IN ('closed','expired','failed')
   AND NOT EXISTS (SELECT 1 FROM sandbox_session_captures c JOIN sandbox_volume_backups b ON b.id=c.volume_backup_id
     WHERE b.session_id=v.session_id AND (c.state='live' OR EXISTS (SELECT 1 FROM sandbox_sessions f WHERE f.fork_capture_id=c.id AND f.state IN ('queued','starting'))))
 ORDER BY v.updated_at, v.session_id LIMIT sqlc.arg(max_rows)::integer;

-- name: ListSandboxVolumeBackupIDs :many
SELECT id::text AS id FROM sandbox_volume_backups
 WHERE session_id=sqlc.arg(session_id)::text::uuid AND state <> 'deleted' ORDER BY created_at, id;

-- name: MarkSandboxVolumeBackupDeleted :execrows
UPDATE sandbox_volume_backups SET state='deleted', deleted_at=now()
 WHERE id=sqlc.arg(backup_id)::text::uuid AND state <> 'deleted';

-- name: DeleteSandboxVolume :execrows
DELETE FROM sandbox_volumes v USING sandbox_sessions sb
 WHERE v.session_id=sqlc.arg(session_id)::text::uuid AND sb.id=v.session_id AND sb.state IN ('closed','expired','failed')
   AND NOT EXISTS (SELECT 1 FROM sandbox_volume_backups b WHERE b.session_id=v.session_id AND b.state <> 'deleted');

-- name: GetSandboxForkSource :one
-- ADR-20261122132055 §3: the session a fork request names, locked so its
-- forks see one state.
SELECT state, (task_kind IS NOT NULL)::boolean AS is_task,
  coalesce((SELECT c.id::text FROM sandbox_session_captures c
    WHERE c.session_id=sb.id AND c.state='live' AND c.purpose='pause'), '')::text AS pause_capture_id,
  (close_requested_at IS NOT NULL)::boolean AS close_requested
 FROM sandbox_sessions sb WHERE sb.id=sqlc.arg(session_id)::text::uuid AND sb.account_id=sqlc.arg(account_id)::text::uuid
 FOR UPDATE OF sb;

-- name: InsertSandboxFork :one
-- A fork is a new session with its source's machine, network, image and
-- volume size, queued until its capture is uploaded.
INSERT INTO sandbox_sessions (account_id,tenant_id,kind,preset,ram_mb,network_mode,idle_timeout_sec,expires_at,labels,
  image_ref,image_job_id,template_id,browser,egress_allow,egress_deny,vcpu,volume_gb,forked_from,fork_capture_id)
SELECT src.account_id, src.tenant_id, sqlc.arg(kind)::text, src.preset, src.ram_mb, src.network_mode, src.idle_timeout_sec,
  sqlc.arg(expires_at)::timestamptz, sqlc.arg(labels)::jsonb, src.image_ref, src.image_job_id, src.template_id, src.browser,
  src.egress_allow, src.egress_deny, src.vcpu, src.volume_gb, src.id, NULLIF(sqlc.arg(fork_capture_id)::text, '')::uuid
 FROM sandbox_sessions src WHERE src.id=sqlc.arg(source_id)::text::uuid AND src.account_id=sqlc.arg(account_id)::text::uuid
RETURNING id::text AS id;

-- name: RequestSandboxSessionFork :execrows
-- A running source's fork capture is agentd's: apid records the request.
UPDATE sandbox_sessions SET fork_requested_at=coalesce(fork_requested_at, now()), updated_at=now()
 WHERE id=sqlc.arg(session_id)::text::uuid AND account_id=sqlc.arg(account_id)::text::uuid
 AND state IN ('starting','ready','frozen','pausing');

-- name: RecordSandboxSessionFork :one
-- vmmd captured the running source: the capture serves the forks waiting
-- for one, and the request is answered.
WITH source AS (
  SELECT id, account_id FROM sandbox_sessions
   WHERE id=sqlc.arg(session_id)::text::uuid AND lease_token=sqlc.arg(lease_token)::text
), capture AS (
  INSERT INTO sandbox_session_captures (id, session_id, account_id, node_id, storage_key, vmstate_storage_key, fc_version,
    mem_bytes, vmstate_bytes, has_backing, volume_backup_id, purpose)
  SELECT sqlc.arg(capture_id)::text::uuid, id, account_id, sqlc.arg(node_id)::text, sqlc.arg(storage_key)::text,
    sqlc.arg(vmstate_storage_key)::text, sqlc.arg(fc_version)::text, sqlc.arg(mem_bytes)::bigint, sqlc.arg(vmstate_bytes)::bigint,
    sqlc.arg(has_backing)::boolean, NULLIF(sqlc.arg(volume_backup_id)::text, '')::uuid, 'fork'
  FROM source
  RETURNING id, session_id
), forks AS (
  UPDATE sandbox_sessions f SET fork_capture_id=c.id, updated_at=now() FROM capture c
   WHERE f.forked_from=c.session_id AND f.fork_capture_id IS NULL AND f.state='queued'
  RETURNING f.id
), answered AS (
  UPDATE sandbox_sessions sb SET fork_requested_at=NULL, updated_at=now() FROM capture c WHERE sb.id=c.session_id
)
SELECT count(*)::bigint AS forks FROM forks;

-- name: ListUnuploadedSandboxForkCaptures :many
-- Captures forks wait for that the shared store may not hold yet: a live
-- fork capture, or a pause capture a queued fork names (its session may have
-- resumed before the upload). agentd checks each and marks it uploaded once
-- the store holds it, and its forks become claimable.
SELECT cap.id::text AS id, cap.session_id::text AS session_id, cap.account_id::text AS account_id, cap.storage_key,
  cap.vmstate_storage_key, cap.has_backing, coalesce(cap.volume_backup_id::text, '')::text AS volume_backup_id
  FROM sandbox_session_captures cap WHERE cap.uploaded_at IS NULL
   AND ((cap.purpose='fork' AND cap.state='live')
     OR EXISTS (SELECT 1 FROM sandbox_sessions f WHERE f.fork_capture_id=cap.id AND f.state='queued'))
 ORDER BY cap.created_at, cap.id LIMIT sqlc.arg(max_rows)::integer;

-- name: MarkSandboxForkCaptureUploaded :execrows
UPDATE sandbox_session_captures SET uploaded_at=now()
 WHERE id=sqlc.arg(capture_id)::text::uuid AND uploaded_at IS NULL;

-- name: ConsumeSpentSandboxForkCaptures :execrows
-- A fork capture no fork waits for any more is used; the capture sweep
-- deletes its objects.
UPDATE sandbox_session_captures cap SET state='consumed', consumed_at=now()
 WHERE cap.purpose='fork' AND cap.state='live' AND cap.created_at < now() - interval '1 minute'
 AND NOT EXISTS (SELECT 1 FROM sandbox_sessions f WHERE f.fork_capture_id=cap.id AND f.state IN ('queued','starting'));

-- name: FailOrphanedSandboxForks :execrows
-- A fork whose source ended before it was captured never starts.
UPDATE sandbox_sessions f SET state='failed', outcome_code='fork_source_ended',
  outcome_message='the session it forked ended before the fork was taken', closed_at=now(), updated_at=now()
 FROM sandbox_sessions src
 WHERE f.forked_from=src.id AND f.state='queued' AND f.fork_capture_id IS NULL
 AND src.state IN ('closed','expired','failed');

-- name: GrowPausedSandboxVolume :one
-- PATCH {volume_gb} on a paused persistent session (ADR-20261122132055 §4):
-- the volume grows when the session resumes, never shrinks. The capture's
-- memory holds the filesystem at its old size, so a grow uses the capture
-- up: the session resumes cold onto the grown volume, its files kept and
-- its processes started again.
WITH old AS (
  SELECT id, coalesce(volume_gb, 0) AS volume_gb FROM sandbox_sessions
   WHERE id=sqlc.arg(session_id)::text::uuid AND account_id=sqlc.arg(account_id)::text::uuid
     AND kind='persistent' AND state='paused' AND coalesce(volume_gb, 0) <= sqlc.arg(volume_gb)::integer
   FOR UPDATE
), grown AS (
  UPDATE sandbox_sessions sb SET volume_gb=sqlc.arg(volume_gb)::integer, updated_at=now()
    FROM old WHERE sb.id=old.id
  RETURNING sb.id, old.volume_gb < sqlc.arg(volume_gb)::integer AS larger
), used AS (
  UPDATE sandbox_session_captures SET state='consumed', consumed_at=now()
   WHERE session_id IN (SELECT id FROM grown WHERE larger) AND state='live'
)
SELECT id::text AS id FROM grown;

-- name: DeleteSandboxSessionCapture :execrows
DELETE FROM sandbox_session_captures cap WHERE cap.id=sqlc.arg(capture_id)::text::uuid AND cap.state='consumed'
 AND NOT EXISTS (SELECT 1 FROM sandbox_sessions f WHERE f.fork_capture_id=cap.id AND f.state IN ('queued','starting'));

-- name: ExpireQueuedSandboxSessions :execrows
-- A session still queued at its end never starts.
UPDATE sandbox_sessions SET state='expired', outcome_code='lifetime', outcome_message='the session ended before it started',
  closed_at=now(), updated_at=now()
 WHERE state='queued' AND expires_at <= now();

-- name: FailImageUnavailableSandboxSessions :execrows
-- A queued session whose image cannot be had never starts: its runner job
-- was evicted or deleted, its image failed to pull, or its template was
-- deleted.
UPDATE sandbox_sessions sb SET state='failed', outcome_code='image_unavailable',
  outcome_message=left(CASE WHEN sb.image_ref LIKE 'template:%'
    THEN 'the template ' || substr(sb.image_ref, 10) || ' was deleted before the session started'
    ELSE 'the image ' || sb.image_ref || ' could not be prepared' ||
      coalesce(': ' || (SELECT j.image_materialization_error FROM jobs j WHERE j.id=sb.image_job_id), '') END, 2048),
  closed_at=now(), updated_at=now()
 WHERE sb.state='queued' AND sb.image_ref IS NOT NULL
 AND NOT EXISTS (SELECT 1 FROM jobs j WHERE j.id=sb.image_job_id AND j.account_id=sb.account_id AND j.status <> 'deleted'
   AND j.image_materialization_status <> 'failed')
 AND NOT EXISTS (SELECT 1 FROM sandbox_templates t JOIN deployments d ON d.app_id=t.app_id
   WHERE t.id=sb.template_id AND t.account_id=sb.account_id AND d.status='live' AND d.rootfs_key <> '');

-- name: SandboxSnapshotBytesUsed :one
-- ADR-20261027090013 step 7: an account's persisted workspaces count their
-- size once ready and their reservation while pending, until agentd deletes
-- them.
SELECT coalesce(sum(coalesce(bytes, reserved_bytes)), 0)::bigint FROM sandbox_workspace_snapshots
 WHERE account_id=sqlc.arg(account_id)::text::uuid;

-- name: InsertSandboxSnapshot :exec
INSERT INTO sandbox_workspace_snapshots (id,account_id,sandbox_id,storage_key,reserved_bytes,expires_at)
VALUES (sqlc.arg(snapshot_id)::text::uuid,sqlc.arg(account_id)::text::uuid,sqlc.arg(sandbox_id)::text::uuid,sqlc.arg(storage_key)::text,
  sqlc.arg(reserved_bytes)::bigint,sqlc.arg(expires_at)::timestamptz);

-- name: CompleteSandboxSnapshot :execrows
UPDATE sandbox_workspace_snapshots SET state='ready', bytes=sqlc.arg(bytes)::bigint, sha256=sqlc.arg(sha256)::text, ready_at=now()
 WHERE id=sqlc.arg(snapshot_id)::text::uuid AND state='pending' AND delete_requested_at IS NULL
 AND sqlc.arg(bytes)::bigint <= reserved_bytes;

-- name: DeleteSandboxSnapshot :execrows
DELETE FROM sandbox_workspace_snapshots WHERE id=sqlc.arg(snapshot_id)::text::uuid;

-- name: GetSandboxSnapshot :one
SELECT id::text AS id,account_id::text AS account_id,sandbox_id::text AS sandbox_id,state,storage_key,reserved_bytes,
  coalesce(bytes, 0)::bigint AS bytes,coalesce(sha256, '')::text AS sha256,expires_at,delete_requested_at,created_at,ready_at
 FROM sandbox_workspace_snapshots WHERE id=sqlc.arg(snapshot_id)::text::uuid AND account_id=sqlc.arg(account_id)::text::uuid;

-- name: ListSandboxSnapshots :many
-- Newest first, without the ones apid asked to delete; a page continues
-- before the (created_at, id) of the last row.
SELECT id::text AS id,account_id::text AS account_id,sandbox_id::text AS sandbox_id,state,storage_key,reserved_bytes,
  coalesce(bytes, 0)::bigint AS bytes,coalesce(sha256, '')::text AS sha256,expires_at,delete_requested_at,created_at,ready_at
 FROM sandbox_workspace_snapshots
 WHERE account_id=sqlc.arg(account_id)::text::uuid AND delete_requested_at IS NULL
 AND (sqlc.narg(before_created_at)::timestamptz IS NULL OR (created_at, id) < (sqlc.narg(before_created_at)::timestamptz, sqlc.narg(before_id)::text::uuid))
 ORDER BY created_at DESC, id DESC LIMIT sqlc.arg(max_rows)::integer;

-- name: RequestSandboxSnapshotDelete :execrows
-- apid's delete is a request; agentd's sweep deletes the archive and the row.
UPDATE sandbox_workspace_snapshots SET delete_requested_at=coalesce(delete_requested_at, now())
 WHERE id=sqlc.arg(snapshot_id)::text::uuid AND account_id=sqlc.arg(account_id)::text::uuid;

-- name: ListSweepableSandboxSnapshots :many
-- What agentd's sweep deletes: archives past their retention, ones apid
-- asked to delete, and reservations never completed within pending_ttl.
SELECT id::text AS id, storage_key FROM sandbox_workspace_snapshots
 WHERE expires_at <= now() OR delete_requested_at IS NOT NULL
  OR (state='pending' AND created_at < now()-make_interval(secs=>sqlc.arg(pending_ttl_seconds)::double precision))
 ORDER BY created_at LIMIT sqlc.arg(max_rows)::integer;

-- name: CountSandboxPorts :one
-- ADR-20261027090013 §11: a session's other exposed ports, against SandboxExposedPortsMax.
SELECT count(*)::integer FROM sandbox_ports WHERE sandbox_id=sqlc.arg(sandbox_id)::text::uuid AND port <> sqlc.arg(port)::integer;

-- name: LockLiveSandboxSession :one
-- Holds a live session's row while its ports change, so a port is never
-- added to a session as it ends.
SELECT state FROM sandbox_sessions WHERE id=sqlc.arg(session_id)::text::uuid AND account_id=sqlc.arg(account_id)::text::uuid
 AND state IN ('queued','starting','ready','frozen') AND close_requested_at IS NULL FOR UPDATE;

-- name: UpsertSandboxPort :one
-- Exposing an exposed port again changes its visibility.
INSERT INTO sandbox_ports (sandbox_id, account_id, port, visibility)
VALUES (sqlc.arg(sandbox_id)::text::uuid, sqlc.arg(account_id)::text::uuid, sqlc.arg(port)::integer, sqlc.arg(visibility)::text)
ON CONFLICT (sandbox_id, port) DO UPDATE SET visibility=EXCLUDED.visibility
RETURNING port, visibility, created_at;

-- name: ListSandboxPorts :many
SELECT p.port, p.visibility, p.created_at FROM sandbox_ports p
 WHERE p.sandbox_id=sqlc.arg(sandbox_id)::text::uuid AND p.account_id=sqlc.arg(account_id)::text::uuid ORDER BY p.port;

-- name: DeleteSandboxPort :execrows
DELETE FROM sandbox_ports WHERE sandbox_id=sqlc.arg(sandbox_id)::text::uuid AND account_id=sqlc.arg(account_id)::text::uuid
 AND port=sqlc.arg(port)::integer;

-- name: GetSandboxPreviewRoute :one
-- What gatewayd-internal caches to route a preview host: the session's
-- placement, its plan, and its exposed ports as port:visibility pairs.
SELECT sb.account_id::text AS account_id, a.plan, sb.state, coalesce(sb.node_id, '')::text AS node_id,
  coalesce(sb.instance_id, '')::text AS instance_id,
  coalesce((SELECT string_agg(p.port::text || ':' || p.visibility, ',' ORDER BY p.port) FROM sandbox_ports p WHERE p.sandbox_id=sb.id), '')::text AS ports
 FROM sandbox_sessions sb JOIN accounts a ON a.id=sb.account_id WHERE sb.id=sqlc.arg(session_id)::text::uuid;

-- name: InsertSandboxTemplate :one
-- ADR-20261027090013 templates: apid creates the build app first, then the
-- template that owns it.
INSERT INTO sandbox_templates (account_id, name, app_id, start_cmd, ready_cmd, ready_timeout_s)
VALUES (sqlc.arg(account_id)::text::uuid, sqlc.arg(name)::text, sqlc.arg(app_id)::text::uuid,
  sqlc.narg(start_cmd)::text, sqlc.narg(ready_cmd)::text, sqlc.arg(ready_timeout_s)::integer)
ON CONFLICT (account_id, name) DO NOTHING
RETURNING id::text AS id;

-- name: UpdateSandboxTemplateCommands :execrows
UPDATE sandbox_templates SET start_cmd=sqlc.narg(start_cmd)::text, ready_cmd=sqlc.narg(ready_cmd)::text,
  ready_timeout_s=sqlc.arg(ready_timeout_s)::integer, updated_at=now()
WHERE account_id=sqlc.arg(account_id)::text::uuid AND name=sqlc.arg(name)::text;

-- name: DeleteSandboxTemplate :one
DELETE FROM sandbox_templates WHERE account_id=sqlc.arg(account_id)::text::uuid AND name=sqlc.arg(name)::text
RETURNING app_id::text AS app_id;

-- name: ListSandboxTemplates :many
-- A template with its build app and that app's newest deployment (the build
-- in progress or last finished) and newest live one (what sessions boot).
SELECT t.id::text AS id, t.name, t.app_id::text AS app_id, a.slug AS app_slug,
  coalesce(t.start_cmd,'')::text AS start_cmd, coalesce(t.ready_cmd,'')::text AS ready_cmd, t.ready_timeout_s,
  t.created_at, t.updated_at,
  coalesce(latest.id::text,'')::text AS build_id, coalesce(latest.status,'')::text AS build_status,
  coalesce(latest.error,'')::text AS build_error,
  coalesce(live.id::text,'')::text AS live_deployment_id, coalesce(live.rootfs_key,'')::text AS live_rootfs_key,
  live.created_at AS live_built_at
FROM sandbox_templates t
JOIN apps a ON a.id = t.app_id
LEFT JOIN LATERAL (SELECT d.id, d.status, d.error FROM deployments d WHERE d.app_id = t.app_id
  ORDER BY d.created_at DESC, d.id DESC LIMIT 1) latest ON true
LEFT JOIN LATERAL (SELECT d.id, d.rootfs_key, d.created_at FROM deployments d WHERE d.app_id = t.app_id
  AND d.status = 'live' AND d.rootfs_key <> '' ORDER BY d.created_at DESC, d.id DESC LIMIT 1) live ON true
WHERE t.account_id = sqlc.arg(account_id)::text::uuid
  AND (sqlc.arg(name)::text = '' OR t.name = sqlc.arg(name)::text)
ORDER BY t.name;

-- name: RecordSandboxTaskResult :execrows
-- ADR-20261116182615 §3: agentd records a sandbox task's end under its lease,
-- once.
UPDATE sandbox_sessions SET task_exit_code=sqlc.arg(exit_code)::integer, task_output=sqlc.arg(output)::bytea, updated_at=now()
 WHERE id=sqlc.arg(session_id)::text::uuid AND lease_token=sqlc.arg(lease_token)::text
 AND task_kind IS NOT NULL AND task_exit_code IS NULL;

-- name: SandboxTaskCallByCall :one
-- The task a gregale_browse call admitted, if it admitted one.
SELECT sandbox_id::text AS sandbox_id FROM agent_sandbox_task_calls
 WHERE endpoint_id=sqlc.arg(endpoint_id)::text::uuid AND thread_id=sqlc.arg(thread_id)::text
 AND tool_call_id=sqlc.arg(tool_call_id)::text;

-- name: InsertSandboxTaskCall :exec
INSERT INTO agent_sandbox_task_calls (sandbox_id, account_id, endpoint_id, thread_id, tool_call_id, issued_at)
VALUES (sqlc.arg(sandbox_id)::text::uuid, sqlc.arg(account_id)::text::uuid, sqlc.arg(endpoint_id)::text::uuid,
  sqlc.arg(thread_id)::text, sqlc.arg(tool_call_id)::text, sqlc.arg(issued_at)::timestamptz);

-- name: ListUndeliveredSandboxTaskCalls :many
-- Ended gregale_browse tasks whose result apid has not yet published into
-- their thread. The mark is written after the publish, which is
-- idempotent, so an interrupted delivery is retried and lands once.
SELECT c.sandbox_id::text AS sandbox_id, c.account_id::text AS account_id, c.endpoint_id::text AS endpoint_id, c.thread_id,
  c.tool_call_id, c.issued_at, sb.state, coalesce(sb.outcome_code, '')::text AS outcome_code,
  coalesce(sb.outcome_message, '')::text AS outcome_message, coalesce(sb.task_kind, '')::text AS task_kind,
  coalesce(sb.task_url, '')::text AS task_url, sb.task_exit_code, sb.task_output
 FROM agent_sandbox_task_calls c JOIN sandbox_sessions sb ON sb.id=c.sandbox_id
 WHERE c.delivered_at IS NULL AND sb.state IN ('closed', 'expired', 'failed')
 ORDER BY sb.closed_at, c.sandbox_id LIMIT sqlc.arg(max_rows)::integer;

-- name: MarkSandboxTaskCallDelivered :exec
UPDATE agent_sandbox_task_calls SET delivered_at = now() WHERE sandbox_id = sqlc.arg(sandbox_id)::text::uuid AND delivered_at IS NULL;

-- name: GetSandboxTaskResult :one
-- GET /v1/sandboxes/{id}/task: a task session's arguments and, once it
-- ended, its exit code and output.
SELECT state, coalesce(task_kind, '')::text AS task_kind, coalesce(task_url, '')::text AS task_url, task_exit_code, task_output,
  coalesce(outcome_code, '')::text AS outcome_code, coalesce(outcome_message, '')::text AS outcome_message
 FROM sandbox_sessions WHERE id=sqlc.arg(session_id)::text::uuid AND account_id=sqlc.arg(account_id)::text::uuid;

-- name: InsertSandboxAction :one
-- ADR-20261117065818 §2: apid records a gregale_browser action on a live
-- session as the session's next, once per tool call.
INSERT INTO sandbox_actions (session_id, account_id, seq, kind, args, endpoint_id, thread_id, tool_call_id, issued_at)
SELECT sqlc.arg(session_id)::text::uuid, sqlc.arg(account_id)::text::uuid,
  coalesce((SELECT max(a.seq) FROM sandbox_actions a WHERE a.session_id = sqlc.arg(session_id)::text::uuid), 0) + 1,
  sqlc.arg(kind)::text, sqlc.arg(args)::jsonb, sqlc.arg(endpoint_id)::text::uuid, sqlc.arg(thread_id)::text,
  sqlc.arg(tool_call_id)::text, sqlc.arg(issued_at)::timestamptz
ON CONFLICT (endpoint_id, thread_id, tool_call_id) DO NOTHING
RETURNING id::text AS id, seq;

-- name: SandboxActionByCall :one
SELECT id::text AS id, session_id::text AS session_id, seq FROM sandbox_actions
 WHERE endpoint_id=sqlc.arg(endpoint_id)::text::uuid AND thread_id=sqlc.arg(thread_id)::text
 AND tool_call_id=sqlc.arg(tool_call_id)::text;

-- name: NextSandboxActions :many
-- Each listed session's next pending action, for the sessions that run none:
-- agentd sends a session one action at a time.
SELECT DISTINCT ON (a.session_id) a.id::text AS id, a.session_id::text AS session_id, a.seq, a.kind, a.args
 FROM sandbox_actions a
 WHERE a.session_id = ANY(sqlc.arg(session_ids)::text[]::uuid[]) AND a.state = 'pending'
 AND NOT EXISTS (SELECT 1 FROM sandbox_actions r WHERE r.session_id = a.session_id AND r.state = 'running')
 ORDER BY a.session_id, a.seq;

-- name: StartSandboxAction :execrows
-- agentd starts a pending action under its session's lease.
UPDATE sandbox_actions a SET state='running', started_at=now()
 FROM sandbox_sessions sb
 WHERE a.id=sqlc.arg(id)::text::uuid AND a.state='pending' AND sb.id=a.session_id
 AND sb.lease_token=sqlc.arg(lease_token)::text;

-- name: FinishSandboxAction :execrows
-- agentd records a running action's end under its session's lease. A nil
-- exit code is an action that never ran to its end.
UPDATE sandbox_actions a SET state='done', exit_code=sqlc.narg(exit_code)::integer, output=sqlc.narg(output)::bytea, finished_at=now(),
  screenshot_key=sqlc.narg(screenshot_key)::text, screenshot_bytes=sqlc.narg(screenshot_bytes)::bigint
 FROM sandbox_sessions sb
 WHERE a.id=sqlc.arg(id)::text::uuid AND a.state='running' AND sb.id=a.session_id
 AND sb.lease_token=sqlc.arg(lease_token)::text;

-- name: EndOpenSandboxActions :execrows
-- Ends a session's actions that will not run to their end: on a fresh
-- claim (a running action's VM is gone) and as the session ends.
UPDATE sandbox_actions a SET state='done', finished_at=now()
 FROM sandbox_sessions sb
 WHERE a.session_id=sqlc.arg(session_id)::text::uuid AND a.state = ANY(sqlc.arg(states)::text[]) AND sb.id=a.session_id
 AND sb.lease_token=sqlc.arg(lease_token)::text;

-- name: SandboxActionThread :one
-- ADR-20261117065818 §3: the thread a gregale_browser session answers (its
-- first action's), its state, and how many actions it has had.
SELECT sb.state, a.endpoint_id::text AS endpoint_id, a.thread_id,
  (SELECT count(*) FROM sandbox_actions c WHERE c.session_id = sb.id)::integer AS actions
 FROM sandbox_sessions sb JOIN sandbox_actions a ON a.session_id = sb.id AND a.seq = 1
 WHERE sb.id = sqlc.arg(session_id)::text::uuid AND sb.account_id = sqlc.arg(account_id)::text::uuid;

-- name: ListUndeliveredSandboxActions :many
-- Ended gregale_browser actions whose result apid has not yet published
-- into their thread. The mark is written after the publish, which is
-- idempotent, so an interrupted delivery is retried and lands once.
SELECT a.id::text AS id, a.session_id::text AS session_id, a.account_id::text AS account_id, a.seq, a.kind, a.args,
  a.endpoint_id::text AS endpoint_id, a.thread_id, a.tool_call_id, a.issued_at, a.exit_code, a.output,
  sb.state AS session_state, coalesce(sb.outcome_code, '')::text AS outcome_code,
  coalesce(a.screenshot_bytes, 0)::bigint AS screenshot_bytes
 FROM sandbox_actions a JOIN sandbox_sessions sb ON sb.id = a.session_id
 WHERE a.state = 'done' AND a.delivered_at IS NULL
 ORDER BY a.finished_at, a.id LIMIT sqlc.arg(max_rows)::integer;

-- name: MarkSandboxActionDelivered :exec
UPDATE sandbox_actions SET delivered_at = now() WHERE id = sqlc.arg(id)::text::uuid AND delivered_at IS NULL;

-- name: GetSandboxAction :one
-- One action of the account's session, for GET /v1/sandboxes/{id}/actions/{seq}.
SELECT a.kind, a.args, a.state, a.exit_code, a.output, coalesce(a.screenshot_key, '')::text AS screenshot_key,
  coalesce(a.screenshot_bytes, 0)::bigint AS screenshot_bytes
 FROM sandbox_actions a
 WHERE a.session_id = sqlc.arg(session_id)::text::uuid AND a.account_id = sqlc.arg(account_id)::text::uuid
 AND a.seq = sqlc.arg(seq)::integer;

-- name: ExpiredSandboxScreenshots :many
-- ADR-20261117065818 §5: screenshots of sessions that ended at least
-- retention_s ago, for agentd's sweep to delete.
SELECT a.id::text AS id, a.screenshot_key::text AS screenshot_key
 FROM sandbox_actions a JOIN sandbox_sessions sb ON sb.id = a.session_id
 WHERE a.screenshot_key IS NOT NULL AND sb.closed_at IS NOT NULL
 AND sb.closed_at < now() - make_interval(secs => sqlc.arg(retention_s)::integer)
 ORDER BY sb.closed_at LIMIT sqlc.arg(max_rows)::integer;

-- name: ClearSandboxScreenshot :exec
UPDATE sandbox_actions SET screenshot_key = NULL WHERE id = sqlc.arg(id)::text::uuid;
-- name: GetSandboxEgressPolicy :one
-- ADR-20261117091026 §2: the account's default web-mode domain policy.
SELECT allow_rules, deny_rules, updated_at FROM sandbox_egress_policies WHERE account_id = sqlc.arg(account_id)::text::uuid;

-- name: PutSandboxEgressPolicy :one
INSERT INTO sandbox_egress_policies (account_id, allow_rules, deny_rules)
VALUES (sqlc.arg(account_id)::text::uuid, sqlc.arg(allow_rules)::text[], sqlc.arg(deny_rules)::text[])
ON CONFLICT (account_id) DO UPDATE SET allow_rules = EXCLUDED.allow_rules, deny_rules = EXCLUDED.deny_rules, updated_at = now()
RETURNING allow_rules, deny_rules, updated_at;

-- name: CountSandboxSessionDomains :one
-- How many grants and refusals a session's domain log holds
-- (ADR-20261117091026 §4).
SELECT count(*) FILTER (WHERE refused = '')::integer AS granted,
       count(*) FILTER (WHERE refused <> '')::integer AS refused
  FROM sandbox_session_domains WHERE sandbox_id=sqlc.arg(session_id)::text::uuid;

-- name: RecordSandboxSessionDomains :execrows
-- Adds entries to a session's domain log while the lease is held and the
-- session has a VM; an entry already there keeps its first sighting.
INSERT INTO sandbox_session_domains (sandbox_id, domain, refused, first_seen)
SELECT s.id, x.domain, x.refused, x.first_seen
  FROM sandbox_sessions s,
       (SELECT unnest(sqlc.arg(domains)::text[]) AS domain, unnest(sqlc.arg(refused)::text[]) AS refused,
               unnest(sqlc.arg(first_seen)::timestamptz[]) AS first_seen) x
 WHERE s.id=sqlc.arg(session_id)::text::uuid AND s.lease_token=sqlc.arg(lease_token)::text AND s.state IN ('ready','frozen')
ON CONFLICT DO NOTHING;

-- name: ListSandboxSessionDomains :many
-- A session's domain log, for its account (ADR-20261117091026 §4).
SELECT d.domain, d.refused, d.first_seen
  FROM sandbox_session_domains d JOIN sandbox_sessions s ON s.id = d.sandbox_id
 WHERE d.sandbox_id=sqlc.arg(session_id)::text::uuid AND s.account_id=sqlc.arg(account_id)::text::uuid
 ORDER BY d.first_seen, d.refused, d.domain;

-- name: ListAgentRunApprovals :many
-- ADR-20261027090016 amendment 2: a run's approvals with their decisions, oldest first.
SELECT a.id::text AS id, a.run_id::text AS run_id, a.tool, a.arguments_sha256, a.arguments_summary, a.created_at, a.expires_at,
  coalesce(d.decision, '')::text AS decision, coalesce(d.note, '')::text AS note, d.decided_at
 FROM agent_run_approvals a LEFT JOIN agent_run_approval_decisions d ON d.approval_id=a.id
 WHERE a.run_id=sqlc.arg(run_id)::text::uuid AND a.account_id=sqlc.arg(account_id)::text::uuid
 ORDER BY a.created_at, a.id LIMIT sqlc.arg(max_rows)::integer;

-- name: GetAgentRunApproval :one
SELECT a.id::text AS id, a.run_id::text AS run_id, a.tool, a.arguments_sha256, a.arguments_summary, a.created_at, a.expires_at,
  coalesce(d.decision, '')::text AS decision, coalesce(d.note, '')::text AS note, d.decided_at
 FROM agent_run_approvals a LEFT JOIN agent_run_approval_decisions d ON d.approval_id=a.id
 WHERE a.id=sqlc.arg(approval_id)::text::uuid AND a.run_id=sqlc.arg(run_id)::text::uuid AND a.account_id=sqlc.arg(account_id)::text::uuid;

-- name: InsertAgentRunApprovalDecision :execrows
-- The decision is written once, and only before the approval expires.
INSERT INTO agent_run_approval_decisions (approval_id, account_id, decision, note, decided_by)
SELECT a.id, a.account_id, sqlc.arg(decision)::text, sqlc.arg(note)::text, sqlc.arg(decided_by)::text
 FROM agent_run_approvals a
 WHERE a.id=sqlc.arg(approval_id)::text::uuid AND a.run_id=sqlc.arg(run_id)::text::uuid
   AND a.account_id=sqlc.arg(account_id)::text::uuid AND a.expires_at > now()
ON CONFLICT (approval_id) DO NOTHING;
