# ADR-601: Binding-checked historical rollback

Status: Accepted

Date: 2026-10-05

## Context

Completed deployments use a readiness-gated historical rollback path, distinct from active canary recovery and service abort. Under stored binding enforcement that path cannot publish unchecked traffic. A successful 202 must confirm durable intent without implying recovery has finished, and delayed readiness must not overwrite a newer release.

## Decision

- Add an exact workflow to `POST /v1/apps/{slug}/rollback`: require both `target_deployment_id` and `expected_current_deployment_id`, accepting UUID or app-scoped revision references. Resolve once. The target must be superseded or live at zero traffic; the expected current must be the completed, sole serving deployment of the same scope. Reject held environment candidates and concurrent rollback intent. Preserve artifact presence/attestation and API contract gates. Reason is bounded, one line. Automatic alert rollback continues to use its legacy contract; enforced scopes require the new exact workflow.
- Store each operation in `deployment_rollback_operations` with immutable selection, bounded status projection, timestamps and a committed routing audit. A partial unique index permits one in-flight operation per app/scope. Commit preparation and the existing snapshot-prime outbox handoff in one transaction. APID never writes instances or calls VM services.
- After existing scheduler readiness and imaged hosting smoke, activate the exact target at explicit zero traffic and mark the operation ready in the same transaction. Start a fresh service readiness clock instead of reusing the historical deployment's timestamp. This makes existing exact binding probes usable without taking traffic from the current release. Explicit historical Git rollback uses the selected artifact, rather than applying mutable branch-head/latest-revision admission intended for new Git pushes.
- A bounded APID worker reads durable operations and evaluates current binding inventory under stored scope policy. It executes no probes or smoke requests. The routing transaction locks the app and operation, rechecks the exact serving pair, current policy/revision and evidence expiry, then publishes traffic and its audit/receipt together. A database traffic trigger and MemStore guard prevent generic traffic updates or other workers from gaining traffic for an active rollback. Failed readiness redeliveries cannot reactivate failed intent.
- For services, reset the historical handoff with an exact current predecessor and request UUID. Permit the explicitly pinned predecessor to be newer than the historical target. Reuse the existing readiness-capacity check, gateway ACK and request-drain handoff. Routing is not completion; persist completion only after the matching promote handoff and audit finish. Cleanup after publication needs no fresh binding grant.
- Pair changes and readiness/artifact failures are terminal; binding blockers remain inspectable and retryable. Bound blocker count and text; receipts contain no secret values or reusable grants. Late blocker writes cannot replace routing/completion. Restart recovery reads durable intent without selecting another target.
- Add authenticated, read-only `GET /v1/apps/{slug}/rollbacks/{operation}`. Return accepted intent inside the additive `DeploymentResponse.rollback_operation` field. Legacy rollback remains compatible when policy is off; enforced scopes reject it before preparation with an exact-workflow hint.
- CLI adds `rollback APP --to vN --expected-current vN [--reason TEXT] [--wait]` and `rollback status APP --operation UUID [--wait]`. Waiting performs GETs only, pins the app/operation/deployment pair, retains blockers on timeout, and succeeds only with complete plus a committed audit/completion timestamp. Interrupt exits 130. Go, Node and Python contracts are additive.

## Validation

Memory/PostgreSQL tests cover exact selectors, concurrent intent/releases, dark readiness, generic-write bypass, stale/wrong-recipient/expired evidence, service capacity, completion barriers, failed readiness redelivery, and late status writes. API tests cover accepted receipts, idempotency, binding blockers and replacement-APID recovery. CLI tests cover one POST followed by GET-only waits, receipt mismatch, timeout blockers and invalid flags. Existing readiness/smoke and service ACK/drain regressions run alongside the new tests.

Native x86_64 Linux KVM `test-metal` and `leakcheck` acceptance remains unrun from this macOS workspace.

### Deployment-owned rollback protocol (2026-11-05)

The target deployment's pinned environment settings and frozen runtime select
the service or request rollback protocol. Shared app edits and a newer desired
head cannot replace that decision. Commit verifies that the durable operation
still matches the target contract before routing, including operations created
by an older process with an incorrect protocol flag. A mismatch fails closed
without moving traffic. Services retain ready-cohort, gateway ACK and drain
barriers; request releases do not wait for service-mode replicas.

Worker readiness also belongs to a particular preparation attempt. imaged passes
the expected deployment stage and worker identity into the activation write. The
store rechecks the stage clock, a matching RUNNING worker, its start time and its
persisted runtime configuration proof against current deployed values before
changing lifecycle or traffic. A retained worker from a previous attempt cannot
satisfy a new rollback, even when its deployment ID is unchanged. Completed
activation notifications remain idempotent; ordinary binding, dependency and
rollback admission checks continue to apply.

Final checked worker cutover repeats readiness under the traffic transaction:
at least one RUNNING worker must have started in the current preparation and
carry a runtime configuration proof matching the target's current inputs. A
stopped worker, missing proof, or stale configuration leaves the predecessor
serving and reports `worker_rollback_not_ready`; a current replacement worker
allows the same operation to continue. Pool reconciliation preserves the
qualification worker while its operation is ready or blocked, and retains the
exact serving predecessor until commit. Holds release after completion or
failure and do not grant additional worker admission.
