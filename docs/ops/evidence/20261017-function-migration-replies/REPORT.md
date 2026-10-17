# Native migration reply-boundary harness

Date: 2026-10-17. Parent revision: `9b87313c0b6fd53776c3d4cf27739fb278f473e2`.
Scope: controlled reply-boundary cases for
[ADR-1352](../../../adr/1547-function-migration-destination.md).

The fault cases and portable guard tests are implemented. No native migration or
native fault case was executed in this workspace. Production remains off.

## Implemented scope

The existing strict native target now requires eight separately deployed cases:
Node 22 and Node 24 each run a baseline, a discarded readiness reply with an
explicit identical adoption retry, an ownership commit whose completed reply is
replaced with an error, and three concurrent identical adoption retries.

Readiness retransmissions reach the real remote vmmd. The coordinator receives
the actual successful retry response. The commit case delegates the actual
PostgreSQL transaction and durable decision lookup through the existing scheduler
recovery flow. Persistent authenticated per-worker callback counts survive
barrier release, so a subsequent replay cannot disappear when the barrier opens.
Retries must preserve the grant/receipt, native process generation, namespace,
eight SQL backend identities and closed execution before commit. Every case
retains the original committed rows and requires source/final app retirement.

Version 2 reports use scope `native-migration-reply-boundaries-v1`. Both Go and
the driver reject missing/duplicate cases and old baseline-only reports. Reports
contain request/reply fingerprints instead of sealed application inputs. The
driver writes `migration-replies.json` and sets `reply_boundaries_qualified` only
after the complete matrix and final native host leak checks succeed.

These controlled after-effect errors do not cut a network socket, lose COMMIT
on the wire or drop guest vsock packets. The fixture explicitly issues adoption
retries; production scheduling continues to roll back adoption errors. This
increment adds no automatic adoption retry policy or release activation.

## Verification

- Selected portable race tests passed with no skips, including real Node
  recovery and a disposable TLS/SCRAM PostgreSQL fixture. They verify four
  interpreters' callback counts across two recovery cycles, session identities,
  phase barriers and rejection of incomplete/replayed evidence.
- In-memory gRPC tests verified three overlapping identical RPCs, unchanged
  request/reply fingerprints, rejection of changed/error replies and cancellation
  before dispatch. These results qualify fixture wiring only.
- The PostgreSQL test-store adapter dispatch test passed, preventing the commit
  fault and decision lookup overrides from being silently bypassed.
- Five Python guard tests passed. Scoped qualification-tag lint reported zero
  issues. Workflow actionlint, docs/live-docs checks, ADR uniqueness, sealed-env
  scope, runbook SQL, shell quoting and text encoding checks passed.
- The complete race-enabled native e2e executable compiled with
  `metal,function_background_native`; suite listing and strict selection guards
  were checked. No native body ran beyond its prerequisite rejection.

See [portable results](portable-results.json),
[native prerequisite attempts](native-checks.json) and
[verification hashes](verification.json). The
[operator runbook](../../function-migration-native-acceptance.md) defines the
prepared fixture and remaining acceptance matrix.

## Limits and cleanup

This workspace runs as UID 1000, has no `/dev/kvm`, no designated native marker
and no prepared two-host configuration. The strict native target rejected those
prerequisites. The required scoped `make test-metal` attempt failed on the missing
staged kernel; `make leakcheck` could not inspect the container's listed native
loop devices. Neither result supplies passing native acceptance.

The task's SQL containers/volumes, credentials, certificates and temporary native
executable were removed. Work remains local; no push, PR, merge or deployment
was performed for this increment.

Actual cross-host execution of these eight cases, real transport loss,
cancellation/expiry/commit races, failed probes, capacity pressure,
host/daemon death and restart, private-environment authority and native leak
acceptance remain unqualified. `failure_matrix_qualified` and `production_ready`
remain false even after this controlled reply-boundary scope eventually passes.
