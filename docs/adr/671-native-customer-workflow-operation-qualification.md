# ADR-671: Native qualification for Customer Workflow Operations

## Status

Harness implemented with admission closed — 2026-10-06. Dedicated native KVM
execution and leak receipts are pending.

## Context

ADR-658–660, ADR-670, and ADR-676 implement workflow ownership, controlled resume,
cooperative execution control and retained private files. Portable state/API
tests cannot prove that real native dispatch preserves these contracts through
guest process death, daemon restart and a change to the default deployment.
Customer Job Operations already have a bounded native lane under ADR-667.

## Decision

Add five native Customer Workflow Operations scenarios to the real app harness.
Use migrated PostgreSQL and apid, schedd, imaged, vmmd, both gateway daemons and
meterd subprocesses. Deploy a static HTTP fixture as an ordinary OCI app with
three linear workflow actions. Seed customer intent and healthy storage accounting;
never synthesize VM instances, native claims, successful step outputs or native
completion. The guest's HTTP replies or process death drive the real workflow
dispatcher. The fixture's confirmed prefix performs no external business effects.

The guest obtains an Operations JWT from guest-init's loopback workload identity
endpoint; vmmd holds the private signing key. Dispatch supplies the native
run/step/generation/attempt/capability proof. Guest control, upload and receipt
lookup use the production API routes through a per-instance test-only vsock relay
to loopback apid. The relay verifies the real JWT and instance, forwards native
proof unchanged, and owns no signing key or execution authority. Bind only native
jail sockets and use their lease UID/GID with mode 0600. Test barrier routes record
business entry and release a holding guest; they do not settle workflow work.

Inject a lost upload acknowledgement after apid commits the verified private
receipt. The guest looks up that receipt without transferring bytes again. On
approved resume, the final action reuses the same artifact ID and private copy.
The recovery scenario exits the real guest process before its final HTTP reply,
restarts apid and schedd after the uncertain outcome is retained, changes the
default deployment and closes new workflow admission. Inspection-fenced recovery
must retain the original run/code/input and confirmed prefix under generation 2.
Separate scenarios cover customer isolation and signed completion delivery retry,
queued/running cancellation, fixed final-action deadline and owner revocation.
Closed native proofs must reject both receipt lookup and a new transfer.

Add a blocking `customer-workflow-operations-only` lane derived from every
top-level test in its scenario file. Missing, skipped or failed selected tests
fail both the native runner and collected-log verdict. Assign the same tests to
the existing full-suite wake phase, which remains blocking. Give the dedicated
lane a 35-minute Go budget inside a 40-minute systemd budget; enlarge wake to
30/35 minutes while keeping the full workflow within its six-hour outer budget.
Each scenario drains its app, stops native producers, deletes only its account's
retained result keys and runs leakcheck. The runner retains its existing host
designation, acceptance lock, preflight/final leak checks and service restoration.

## Validation and rollout

Run the guest fixture behavior tests explicitly in portable CI because `testdata`
is excluded from `go test ./...`. Check its workload identity, native proof,
lost acknowledgement, process-death injection, cancellation and deadline behavior;
verify the static OCI image command and the source-derived CI selection/verdict
contracts. Compile the metal package on development hosts without executing it.

Qualification requires an exact committed source run on the designated native
x86_64 Linux KVM host, all five PASS receipts, native leak receipts and successful
service restoration. The test-only relay does not qualify public DNS/TLS ingress
or the production guest-to-API network path. Result bytes use the acceptance host's
configured private artifact backend; provider failure and fleet rollback drills
remain separate gates. No VM lifecycle, production policy or storage schema is
changed. Production workflow admission remains closed until qualification.

See [the native qualification guide](../ops/customer-workflow-operations-native.md).
