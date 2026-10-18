# Operator build-to-image handoff observation

Status: accepted

## Context

A succeeded build can still wait for imaged to consume its artifact. Existing
export pressure and two-hour stale-deployment warnings do not measure this wait.
Recovery handles 16 resumable preparations and 16 untouched handoffs synchronously
per pass; list and handler failures mostly appear in logs. Spec section 12 supports
these diagnostics. Section 17 and Appendix D require no execution, ownership,
frontend or rollout change for this observation.

## Decision

Add a narrow optional store returning count and oldest build finish time in one
SQLC aggregate or one MemStore lock. Match ListBuildsAwaitingImage eligibility:
succeeded build with provenance, nonempty deployment artifact path, pending or
building deployment, and no preparation checkpoint. Preserve node compatibility:
configured readers see their producer node and legacy empty owners; empty readers
retain all-owner scope. Normalize the reader identity. Do not apply the 16-row
batch limit to observations. Missing/nonfinite finish times reject the snapshot.
Count eligible build rows, not fleet deployments, and never sum overlapping scopes.

Checkpointed work is excluded even while active or resumable. Retries must not
renew an untouched build's wait age. This is not a snapshot-preparation backlog
or readiness signal. Start an independent sequential observer with Loop.Run,
before synchronous recovery. Read immediately and every 30 seconds, with a
three-second deadline and two-minute freshness ceiling. Stop its context when
Run returns. Scrapes read RAM only. Count and oldest finish time require a valid,
recent successful read; read success and frozen last-success time remain separate.
Stamp read start conservatively, validate against completion time, and never
convert missing, failed, expired, future or inconsistent evidence into zero work.

Count recovery list calls by source `resume|awaiting` and outcome
`success|error|canceled`. Count actual handler returns separately as
`returned|retryable|deferred|not_owned|error|canceled`. Busy artifact leases and
protective claim/state fences are deferred. Caller cancellation must match the
caller context. Nil return is returned, not proof of a durable transition.
Preserve query order, scheduling, batch limits, calls, return values and fences.

Count successful fenced checkpoint writes only after the store call returns:
`claimed`, `layer_published`, `scan_complete`, `handed_off`. Claimed includes
resumed tokens and is not unique work or completed image progress. Handed_off
can transfer ownership to an admitted release task and is not readiness.
Terminal/raced no-ops invent no commits. Shared counters include direct OCI image
preparation; the waiting snapshot itself covers succeeded source builds only.
Use no build, deployment, account, artifact-path or arbitrary error labels.

Expose pass attempt/completion times, inflight state and expectation. Completion
means the pass returned, including errors/no-ops. Synchronous image work and
other handlers occupy the event loop, so do not infer a stalled daemon from the
two-second ticker. Cancellation/loop return withdraws expectation without
fabricating drain or completion. A blocked preparation cannot freeze the observer.

Add two recordings for complete/fresh evidence and oldest waiting age. Three
internal warnings: unknown telemetry after five minutes; waiting age above five
minutes held five minutes; list errors above 10% of non-canceled calls with at
least three errors in each rolling five-minute window, continuously for five
minutes. Keep sources and instances separate. Require coherent timestamps,
counts, phases and complete initialized counter vocabularies. Failed scrapes use
target-health alerts; explicit disabled expectation suppresses these warnings.
Waiting behind healthy serial work is still delay, not a stalled-loop diagnosis.
Customer image failures, claim fences and busy leases cannot trigger query alerts.

## Validation and adversarial review

Compare memory and real PostgreSQL observations to the actual recovery predicate,
including more than one batch, foreign/empty owners, checkpoint creation/reclaim,
publication, canceled/failed rows, missing artifacts, missing/nonfinite finish
times and canceled reads. Race-test freshness/failures, bounded reads, concurrent
nonblocking scrapes and Loop.Run collection during blocked image work. Verify
failed publication does not increment committed phases, busy leases defer then
recover, and existing crash/restart cases commit the final handoff once.

Promtool fixtures cover exact age/hold boundaries, age without new arrivals,
healthy long preparation, source/instance isolation, query failure volume/ratio,
transient bursts, invalid/missing/expired evidence, shutdown, recovery, failed
scrapes and absent installations. Refresh catalogs and preserve existing rule
fingerprints. Run codegen, affected tests, lint and repository policy checks.

Adversarial review focuses on mistaking an empty batch for empty durable state,
counting active preparation as waiting, renewing age on retries, treating nil
returns as commits, flagging long work as a stopped loop, masking query failures,
summing overlapping legacy scopes, and adding reads to scrapes or serialized work.
