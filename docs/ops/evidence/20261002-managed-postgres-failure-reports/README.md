# Managed PostgreSQL failure reports: internal KVM diagnostics

Date: 2026-10-02.

ADR-397 persists liveness and workload OOM reports on vmmd, retries transient
failures and requires an explicit applied acknowledgement from schedd. The
real-guest regression injects failed scheduler stop RPCs, checks retained guest
ownership/admission, reopens the outbox with the same Manager, then verifies
confirmed teardown through the real schedd gRPC handler.

These are nested-node diagnostics, not supported native x86_64 KVM acceptance.
The regression uses MemStore and a simulated failed Engine→Manager stop; the
liveness/OOM observations are injected. It does not prove OOM detection, durable
PostgreSQL state recovery, full vmmd crash inventory or all-node drain receipts.
Customer cutover activation remains disabled.

## Source and host

- Final validation snapshot: `040ed52d0e0d2d8609a6c8c590454704a543803a`, tree `21e66dfbe1d8ce124e1c2c4d787917455e626bfc`.
- Parent: `723b2f016f6ffe114ca92df3632fd8c85ac4ce49` (ADR-396).
- Selected base archive from `d598bb27fae873d0b9215ae5b3c28311e5e53396`:
  `fcb6804f9168578468cf7ea5c5aabca7f28fd53a45e4ef970cfde59033be1d9c` (13,414,504 bytes).
- Ownership supplement from `a902715766ab41566a8755870022ebf2d2774ba7`: `09876fa7df2950b0defdd2c7dc4543577d5b6d7d3b1f9bf473ecc2d4cab55f6c`
  (97,409 bytes), containing exactly the three changed paths in
  `validation.json`. Source-node supplement from the final snapshot: `ef12629e6d4edf7fc53c8960415bd4224e7f7044ee8952b1a028443a051c552f`
  (248,990 bytes), with paths in `validation.json`.
  These three ordered archives reconstruct the selected final source. All archives and the
  runner passed checksums before dispatch; archives were checked again before
  extraction. All committed Go files and
  go.mod/go.sum match this snapshot; later edits add documentation/evidence only.
- Project `gregale-prod`, zone `us-east1-b`, node `gregale-internal-test-1`,
  labels environment=test, fleet=excluded, purpose=internal-tests.
- x86_64 Linux `7.0.0-1011-gcp`, `/dev/kvm`, Go `1.25.13`,
  Firecracker/jailer `1.7.0`, n2-highmem-2; nested virtualization enabled.
- Reference SSD latency acceptance disabled. No production deployment, customer
  database binding change or cutover activation.

The runner requires root, the acceptance-host marker, the shared acceptance lock
and inactive Gregale VM/build/image/gateway services. Root disk is nearly full;
owned stage storage uses a six-GiB tmpfs and private copied module/build caches.
The transient unit provides a four-GiB build TMPDIR and one-GiB jail filesystem,
with MemoryMax=10G. `runner.sh` and `dispatch-command.txt` preserve the invocation.
Exact-source static guest-init and immutable two-drive busybox HTTP fixtures were
built. Transfers completed before dispatch.

## Results and scope

`make test-metal` ran `./pkg/fcvm ./pkg/sched ./pkg/vmmd/failureoutbox
./pkg/scheddgrpc ./cmd/vmmd` with `-race -count=1`, the explicit RUN_REGEX in
`runner.sh` and `-timeout=20m -v`. It selected new persistence/replay, source
binding and wire-ack regressions, existing liveness/OOM RPC contracts, scheduler
accounting cases and real teardown dependencies. Unselected tests and the
unfiltered native acceptance suite are outside this evidence.

| Selected checks | Passed | Skipped | Failed |
| --- | ---: | ---: | ---: |
| Top-level tests | 34 | 0 | 0 |
| Including subtests | 127 | 0 | 0 |

The second row includes the first; do not add them. The new real-guest outbox
regression passed both liveness and workload OOM cases in 5.03 s. It observes
multiple failed attempts without releasing the guest or admission, reopens the
spool with the same Manager and verifies automatic successful teardown through
the actual schedd RPC handler. Full vmmd crash inventory remains outside scope.

fcvm completed in 10.995 s, scheduler in 1.270 s, outbox in 2.102 s, schedd RPC
in 1.723 s and vmmd in 1.133 s. metal.exit=0 and all three in-run leak checks
passed under the shared lock. The completion poll observed inactive/dead,
Result=success, ExecMainStatus=0; the transient unit was subsequently collected
(LoadState=not-found in unit-final.txt). No extra host-wide check was run after
releasing the lock.

Logs were downloaded before cleanup. The owned stage was unmounted; canary cleanup recorded state=completed and
removed its directory. The final unit is inactive/dead and collected. Initial
failed-unit state was reset. Other jobs, services and caches were preserved.


The initial run passed the real-guest, scheduler, outbox and schedd RPC checks
but failed to compile cmd/vmmd because the existing metal-only workload OOM test
declared an unused ctx at line 109 (originating in commit 24356cfbdb). The minimal
compile repair and source-node binding were included in the final pinned rerun.
`first-run.log`, `first-metal.log` and `first-unit.txt` preserve that failure.

## Portable checks and limitations

Complete final race checks passed for scheduler, outbox, vmmd, schedd RPC and
fcvm, including rejection of direct and relayed reports from a previous node. Complete
unit generator/specification race checks also passed; the final additional
ownership regression passed after excluding unknown-ID stop reservations. Those
reservations serialize stops without identifying a guest and cannot authorize
recovered reports. Changed-code lint reports zero issues. macOS linker warnings
about LC_DYSYMTAB were nonfatal; they are retained in the raw/normalized logs.

An initial unit-specification run exposed the pre-existing undeclared
FAAS_MANAGED_POSTGRES_QUALIFIED_VERSION. Its declaration and generated operator
reference were repaired; the initial ordering/doc-sync failure and passing final
run are both retained. Text encoding, shell quoting, sealed-env scope, ADR
uniqueness, generated units and changed core-test citations passed. No new Go
dependency or database migration was introduced. Protobuf field additions were
regenerated with the repository toolchain.

Spool replay after a vmmd crash is fail-closed when the new Manager lacks the
original resource identity. Reports remain pending until guest ownership is
reconciled; this slice does not reconstruct that inventory. Reports retain the
observer node across retries. Node binding rejects a delayed
report after a completed move; concurrent migration and same-node incarnation
fencing still require ownership epochs. Delivery configured without a resolvable
compute identity fails startup; DB-less development can omit the schedd target.
Storage failures retain in-memory retries, but crash durability requires a
successful disk commit.
Applied acknowledgements are observation outcomes, not fleet drain proof. The
remaining cleanup audit, ownership epochs, SQL-session fencing and cutover
publication are separate work.

Committed text normalizes carriage returns and trailing whitespace only.
SHA256SUMS covers committed evidence; RAW_SHA256SUMS records original bytes,
retained in the local task checks directory. A credential-marker scan of the
downloaded and committed logs found no matches.
