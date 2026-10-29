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
  model_integrations,ai_budget_millicents,deliveries)
 VALUES (sqlc.arg(agent_id)::text::uuid,sqlc.arg(version)::integer,sqlc.arg(account_id)::text::uuid,
  sqlc.narg(harness_preset)::text,sqlc.narg(harness_image)::text,sqlc.arg(harness_run)::text[],sqlc.arg(harness_models)::jsonb,
  sqlc.arg(prompt_template)::text,sqlc.narg(ram_mb)::integer,sqlc.arg(timeout_sec)::integer,
  sqlc.arg(model_integrations)::jsonb,sqlc.narg(ai_budget_millicents)::bigint,sqlc.arg(deliveries)::jsonb)
 RETURNING version,harness_preset,harness_image,harness_run,harness_models,prompt_template,ram_mb,timeout_sec,created_at,
  model_integrations,ai_budget_millicents,deliveries;

-- name: GetAgentVersion :one
SELECT version,harness_preset,harness_image,harness_run,harness_models,prompt_template,ram_mb,timeout_sec,created_at,
  model_integrations,ai_budget_millicents,deliveries FROM agent_versions
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

-- name: TryLockAccountAgents :one
-- The same per-account key apid admits under; agentd never waits for it, so
-- an apid transaction that holds it and wants a run row cannot deadlock.
SELECT pg_try_advisory_xact_lock(hashtextextended('agent-runs:' || sqlc.arg(account_id)::text, 0))::boolean;

-- name: AccountAgentLoad :one
SELECT a.plan,
  (SELECT count(*) FROM agent_runs r WHERE r.account_id=a.id AND r.state IN ('starting','running','delivering'))::integer AS live
 FROM accounts a WHERE a.id=sqlc.arg(account_id)::text::uuid;

-- name: StartAgentRun :execrows
UPDATE agent_runs SET state='starting', lease_token=sqlc.arg(lease_token)::text, lease_owner=sqlc.arg(lease_owner)::text,
  lease_expires_at=now()+make_interval(secs=>sqlc.arg(ttl_seconds)::double precision), started_at=now(), updated_at=now()
 WHERE id=sqlc.arg(run_id)::text::uuid AND state='queued';

-- name: GetAgentRunClaim :one
SELECT r.id::text AS id, r.account_id::text AS account_id, a.plan, r.agent_id::text AS agent_id, ag.name AS agent_name,
  r.agent_version, coalesce(r.tenant_id::text, '')::text AS tenant_id, r.prompt, r.inputs, r.ram_mb, r.timeout_sec, r.attempt,
  v.harness_preset, v.harness_image, v.harness_run, v.harness_models, v.prompt_template, v.model_integrations, v.ai_budget_millicents,
  r.checkout_repository, r.checkout_commit, r.checkout_installation_id
 FROM agent_runs r JOIN accounts a ON a.id=r.account_id JOIN agents ag ON ag.id=r.agent_id
  JOIN agent_versions v ON v.agent_id=r.agent_id AND v.version=r.agent_version
 WHERE r.id=sqlc.arg(run_id)::text::uuid;

-- name: MarkAgentRunRunning :execrows
-- The at-most-once fence (ADR-20261027090010 §4): from here a lost lease fails the run.
UPDATE agent_runs SET state='running', running_at=now(), node_id=sqlc.arg(node_id)::text, instance_id=sqlc.arg(instance_id)::text,
  ai_budget_millicents=sqlc.arg(ai_budget_millicents)::bigint, updated_at=now()
 WHERE id=sqlc.arg(run_id)::text::uuid AND lease_token=sqlc.arg(lease_token)::text AND state='starting';

-- name: RecordAgentRunBoot :execrows
UPDATE agent_runs SET boot_ms=sqlc.arg(boot_ms)::integer, updated_at=now()
 WHERE id=sqlc.arg(run_id)::text::uuid AND lease_token=sqlc.arg(lease_token)::text AND state='running';

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
