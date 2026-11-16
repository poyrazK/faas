# Binding verification runs

A verification run checks many bindings at once and survives interruptions. Start one for an app or a project graph, review its plan, and resume or recover it from the CLI or an SDK.

## Parallel binding qualification

`gregale app binding verify APP --release ID --release-policy --concurrency 2`
runs independent required probes concurrently. The default is 2; `--concurrency`
accepts 1–8 and applies only to release-policy verification. Use 1 for serial
execution when provider or application capacity requires it. Server admission
limits still apply.

`gregale environment release-sets verify PROJECT ENV --file release.json
--concurrency 2` shares the same limit across all graph members. Review with
`--dry-run`; planned object writes still require `--object-storage-write`, and
planned queue publication still requires `--queue-publish`. No probe starts until
every member has passed the plan and consent review.

The CLI preserves reviewed deployment, configuration, policy and service target
snapshots. Results stay in plan order. Failed or interrupted runs stop new
admissions and wait for cleanup of work they created; preexisting queue canaries
remain available to their original owners. After all members finish, graph
verification rechecks current evidence against the original snapshots. Evidence
expiry and the atomic checks at activation remain unchanged.

## Durable single-app binding verification

Use a durable receipt when a CI job or CLI session may disconnect:

```sh
gregale app binding verify my-api --release v12 --release-policy --durable \
  --object-storage-write --queue-publish --concurrency 2 --json
```

Only include `--object-storage-write` or `--queue-publish` when you consent to
those planned effects. Review with `--dry-run` first; a dry run creates neither
a receipt nor a resource. `--durable` requires `--release-policy` and a wait
budget of at least one second. The lifetime is capped at ten minutes. To make
creation recovery deterministic in CI, supply your own UUID with `--run-id`.
The CLI prints the run ID before requesting creation and includes `run_id` in
its verification report.

```sh
gregale app binding verification status my-api RUN_ID --json
gregale app binding verification resume my-api RUN_ID --concurrency 2 --json
gregale app binding verification cancel my-api RUN_ID --json
```

Status shows the original plan, resource IDs, ownership and current progress.
Receipt details require the same domain catalog access as their binding inventory.
Resume keeps the original deployment, policy, configuration, binding metadata,
consent and deadlines. It observes compatible pending work and fresh results;
failed, missing, cancelled, changed or expired work requires a newly reviewed run.
It does not silently publish another message or replace a task. A shorter resume
wait budget is allowed and never renews the server deadline.

Each new task/message and its receipt entry commit together. If the client crashes
or loses that HTTP response, the next admission request returns the existing
resource. Transient transport errors preserve owned work for resume. An explicit
cancel, interrupt or exhausted CLI wait budget blocks new admissions and requests
cleanup of unfinished owned work. Preexisting work remains owned by its original
creator. In-flight handling may still finish; cancellation is cooperative.

Admission and publication remain insufficient for qualification. The server only
completes a run after successful owned work and fresh policy preflight. Current
proof can subsequently expire or change; activation still performs its atomic
policy and freshness checks. Runs are limited to 128 steps and eight unfinished
unexpired receipts per app, use normal queue/runtime capacity, and are pruned after
one day of expiry on the next creation for that app. This workflow currently covers
one app; graph verification retains its existing prepared-manifest workflow.

## Durable project graph verification

Persist the prepared graph and every member's review before admitting probes:

```sh
gregale environment release-sets verify my-project production \
  --file prepared.json --durable --run-id RUN_UUID \
  --queue-publish --object-storage-write --concurrency 2 --json
```

Only include effect consent flags for effects you reviewed. `--dry-run` previews
all members without creating receipts, tasks or messages. Durable verification
also accepts prepared rollback and deactivation manifests. It requires a wait
budget of at least one second, capped at ten minutes. The parent and every member
receipt commit together and share that fixed deadline. The CLI prints the parent
ID before creation, so a lost creation response is recoverable with the same UUID.

```sh
gregale environment release-sets verification status my-project production RUN_UUID --json
gregale environment release-sets verification resume my-project production RUN_UUID --concurrency 2 --json
gregale environment release-sets verification cancel my-project production RUN_UUID --json
```

Resume retains membership, routing expectations, policy revisions, exact service
recipients, effect consent and resource ownership. Changed membership, private
configuration, failed work or expired evidence requires a new review. Retries use
the recorded member and resource IDs. Explicit cancel, interrupt or exhaustion of
the root CLI wait budget fences the graph before cooperative cleanup of unfinished
owned work. Transient transport errors preserve work for recovery.

Completion freshly checks all members after their probes finish. Graph passed
status does not authorize activation: activation still performs its atomic graph
and binding checks. Graph receipts use the existing 100-member and per-member
admission bounds, at most eight active graph runs per project/environment, and
normal runtime/queue capacity. Receipt requests are limited to 1 MiB. Receipts
older than one day after expiry are pruned on the next creation for the project.
GitOps candidate holds retain their existing checked workflow.

## Review a binding verification plan

App and project graph verification reviews are side-effect-free reads:

- `POST /v1/apps/{slug}/bindings/verification-runs/review` accepts an optional deployment ID and exact service targets. An omitted deployment selects the current live deployment and returns its exact ID.
- `POST /v1/projects/{slug}/environments/{environment}/release-sets/verification-runs/review` accepts a prepared release, rollback or deactivation manifest and the intended effect consent flags.

Responses contain public inventory, policy, the plan, blockers, required consent and opaque review digests. `ready` means the plan is available; it does not mean probes or release checks passed. Reviews never run probes, publish messages or create receipts. Blocked reviews have no usable digest.

Forward the returned deployment, targets and digest into app receipt creation. For graph creation, forward the manifest, member `review_digests`, aggregate `review_digest` and the same consent flags. Creation rechecks current state and rejects a stale or altered review. CLI `--dry-run` uses these endpoints; durable verification reviews on the server before creating its receipt.

## Run an existing verification receipt from an SDK

Go provides `Client.RunBindingVerification` and `Client.RunProjectBindingVerification`. Node provides `runBindingVerification` and `runProjectBindingVerification`. Python provides `run_binding_verification` and `run_project_binding_verification`, plus async `arun_` variants.

Save the caller-selected receipt UUID before creation. After forwarding the server review and explicit consent to creation, pass that UUID to the runner. It resumes frozen steps, observes task/canary status and asks the server for final qualification. Success requires a confirmed passed completion; graph completion also requires every member to pass. Verification never activates routes.

The default concurrency is two unfinished resources, configurable from one to eight across the entire graph. Existing work counts toward that budget. A runner never replaces reused work or renews expiry. Multiple runner invocations do not share a distributed client lease; server admission and capacity limits still apply.

SDK transport failures, caller stops and progress callback failures preserve server work and identify the same run for resumption. Use the existing cancel API when cancellation is intended. Go context cancellation, Node abort signals and Python stop events stop observation; asyncio task cancellation carries `run_id`. Synchronous Python stops are checked between requests, whose configured HTTP timeouts are bounded by the remaining receipt lifetime. The CLI retains its interrupt/deadline cleanup policy. Durable CLI JSON reports include sanitized `resource_id`, `resource_kind` and status without reconstructing guest stage reports.

## Explain blocked verification receipts

Use `gregale app binding verification diagnostics APP RUN_ID --json` or `gregale environment release-sets verification diagnostics PROJECT ENVIRONMENT RUN_ID --json` to inspect blockers and recovery suggestions. The corresponding GET endpoints end in `/verification-runs/{run}/diagnostics` on the app binding or project environment release-set surface.

Responses preserve the original run ID and expiry and include `complete`, optional `check_passed`, `blockers`, `changes` and `recovery`. `complete` means applicable diagnostic reads succeeded; `check_passed` describes current public policy/evidence. Neither grants admission, confirms receipt completion or authorizes traffic changes. A previously passed or expired receipt can have current blockers.

Differences contain only whitelisted public values such as policy revisions, configuration timestamps, credential generations and selected service target IDs. Credentials, provider origins, private commands/pins, raw errors and guest stdout are excluded. Incomplete reads produce retry guidance rather than false binding-removal differences. Access to every reviewed binding domain is required, including when the original deployment is unavailable.

Recovery actions distinguish continuing the original run, waiting for application acknowledgment/readiness, retrying diagnostics, explicitly cancelling unfinished owned work, and reviewing a new receipt. Changed original authority suppresses resume guidance. Diagnostics perform no execution or cleanup and never renew expiry. Get fresh diagnostics or invoke the existing resume/cancel APIs deliberately; those operations independently enforce permissions and current authority.

## Shared verification admission budgets

Receipt creation accepts `max_concurrency` (one through eight, default two). The CLI freezes `--concurrency` into a newly created durable receipt. Every driver of that receipt shares the server budget, and project graph members share one root budget. Resuming a receipt cannot change it; the runner's local concurrency may further restrict admission attempts. Independent receipts retain separate budgets, and normal scheduler, queue and account capacity still apply.

At capacity, step admission returns HTTP 429 with code `binding_verification_budget_full`, `limit` and `observed`. Current SDK runners wait, reread the same receipt, and retry within its original deadline. Other capacity errors still stop observation with the original receipt ID. An admission replay returns its original resource even when the budget is full.

Diagnostics include `budget.run_id`, `max_concurrency`, `active_resources`, `waiting_steps` and `full`. Counts deduplicate resource identities and include borrowed unfinished work. Cancellation requests or expired canary evidence do not free a slot while the underlying task/invocation remains unfinished. Terminal or missing resources free capacity; they do not count as successful verification.

## Automatic recovery explanations

Durable verification failures now include best-effort diagnostics in CLI JSON output under `diagnostics`. Project failures include root diagnostics and available member diagnostics in `reports`. Terminal output shows a short blocker/recovery summary. The original failure, exit code and receipt ID remain authoritative for the client outcome; successful diagnostic reads never make verification pass.

Go recovery errors expose `Diagnostics` or `ProjectDiagnostics`. Node and Python recovery errors expose a typed `diagnostics` value. Lookups are read-only and bounded to two seconds, the caller deadline and a confirmed original receipt expiry. Cancelled calls and local callback failures skip enrichment; unavailable, forbidden or mismatched responses leave the original error intact. `SkipDiagnostics`, `skipDiagnostics` or `skip_diagnostics` disables enrichment in the corresponding SDK. Manual `verification-runs diagnostics` remains available. Suggested recovery is never executed automatically.

## Create and reconcile reviewed verification receipts

SDK create-from-review helpers accept an explicit ready review, caller-supplied canonical UUID, lifetime, concurrency and effect consent. Persist the UUID before calling creation. Helpers validate the review and returned selection without computing digests. Server freshness, authorization and idempotency checks remain authoritative.

A creation error preserves the original cause and UUID plus whether creation was invoked; this does not prove acceptance. Use the separate reconciliation helper to GET that same ID once and validate the receipt against the same review/options. A failed or missing read does not prove noncreation. Reconciliation permits terminal or expired matching receipts and performs no resume, admission, renewal or cancellation. Run a confirmed resumable receipt explicitly with the existing runner.

CLI creation failures with `--json` include `run_id`, app or project/environment, `passed=false`, `status=creation_failed`, `create_attempted`, `error` and recovery guidance. Inspect the same ID with `verification-runs status` after uncertainty.

## Retry transient verification status reads

Durable runners retry temporary polling GET failures and HTTP 502/503/504 up to three additional times per read. Backoff uses equal jitter with exponential delay from 250 ms, capped at two seconds, and obeys the original receipt expiry and caller cancellation/deadline. Lifecycle writes, diagnostics, permanent HTTP failures and malformed or mismatched successful responses retain their existing recovery behavior. Exhaustion returns the existing recovery error and diagnostic enrichment.

Set `--status-read-retries 0` on durable CLI verify or receipt resume to disable; values through eight are supported. Go options use `StatusReadRetries` (nil defaults to three, pointer to zero disables), Node uses `statusReadRetries`, and Python uses `status_read_retries`. Progress callbacks receive root `retrying` events with `retry_attempt` and `retry_delay_ms`. No new receipt, renewed expiry or duplicate admission is introduced.

## Discover retained verification receipts

Use `gregale app binding verification list APP --json` or `gregale environment release-sets verification list PROJECT ENVIRONMENT --json` when you need to recover a receipt ID. Lists are read-only summaries; they do not refresh evidence, admit work, resume or cancel receipts. A listed passed state does not establish current qualification.

Pages default to 20 entries (maximum 100), newest first by creation time and UUID. Use `--limit`, `--cursor`, `--deployment UUID` and `--status pending|passed|cancelled|expired`. App lists additionally accept `--scope` and `--include-members`; by default they omit graph members. Project lists show root receipts and match deployment filters against reviewed members. Included app members expose their original `graph_run_id`.

Reuse filters with `next_cursor`; `observed_at` freezes expiry evaluation across pages. Newer creations do not shift existing page boundaries. Concurrent state changes and retention pruning can remove matches, so pages are not a historical snapshot or permanent audit log. Existing receipt cleanup is unchanged. Inspect the selected UUID with status/diagnostics before choosing a recovery action.

## Waiting for final binding qualification

Durable app and graph runners now keep observing the original receipt when completion returns HTTP 202 with recognized `qualification` conditions. Progress callbacks receive `waiting_for_qualification` and public condition codes/selectors. This covers runtime refresh, consumer readiness and valid application acknowledgment observations that are still settling. Failed or malformed evidence, unknown conditions and review drift still stop the run.

Waiting uses the existing polling interval, caller stop controls and original expiry. It admits no replacement tasks or queue messages and never renews a receipt. CLI verify/resume inherits the shared runner behavior and existing timeout cleanup policy. HTTP 200 with passed status still requires the authoritative final preflight.

## Recovering lost admission and completion responses

Durable runners reconcile eligible transport failures and HTTP 502/503/504 by reading the original receipt. Admission proceeds only when that read proves the exact reviewed step owns a valid resource. Completion succeeds only when fresh status confirms the root and all graph members passed with finished resources. Pending completion, missing ownership, failed resources or review drift stop with the original receipt ID and error cause retained.

Progress reports `reconciling` with `operation: admission` or `operation: completion`; admission events identify the selected member and step. Reads use `--status-read-retries` and share the original deadline and caller stop controls. The uncertain write is never automatically replayed. Create, initial resume and cancellation keep their existing explicit recovery behavior.

## Binding CLI progress and stopped-run recovery

Durable app/graph verify and receipt resume accept `--progress auto|plain|ndjson|off`. Auto prints compact stderr updates for human output and disables live updates under `--json`. Explicit progress flags require durable execution and cannot be used with dry-run, status, diagnostics or cancellation. Planned steps without resources are omitted from plain updates; repeated conditions with only a newer observation time are suppressed.

For CI, keep stdout as one final JSON report and stream typed events on stderr:

```sh
gregale app binding verify api --release-policy --durable --json --progress ndjson
gregale environment release-sets verification resume project production RUN_ID --json --progress ndjson
```

`binding_verification_progress` events include root/member IDs, step/resource status, `waiting_for_qualification` conditions, read retries and `reconciling` operations. Graph events also identify project and environment. During execution, cleanup failures use `binding_verification_notice` with code `cleanup_failed`. Pre-execution argument/authentication failures retain the normal CLI error contract.

Stopped app reports and graph result envelopes include `recovery` with the original root ID, last status/operation, `last_qualification` and exact status/diagnostics commands. These conditions are the last observed wait, with its original check time; they do not assert current qualification. Human summaries show the same recovery context. Turning progress off does not discard it. Creation still announces its recovery ID before the request, using a structured `creating` event in NDJSON mode. Progress output is best effort and never changes execution or timeout cleanup.

## Recovering an uncertain verification creation

Durable app/graph CLI verification now reconciles eligible creation transport failures and HTTP 502/503/504 by reading the original caller-selected UUID. It continues only when the receipt matches the reviewed selection, manifest, member identities, consent, concurrency, plans and lifetime and remains usable. A missing receipt, expired/cancelled state, failed resource or changed selection stops with the original creation error and recovery ID. Creation is never automatically posted twice, and no replacement UUID or review is generated.

Recovery is capped at five seconds, the original caller deadline/stop controls and the requested TTL measured from the attempt. `--status-read-retries` bounds extra transient GET attempts; zero allows the initial reconciliation GET without retries. Existing progress modes emit `reconciling` with `operation: creation` and `retrying` when needed. `--json --progress ndjson` keeps the final report on stdout and events on stderr. The normal runner then resumes the same confirmed receipt under its original expiry.
