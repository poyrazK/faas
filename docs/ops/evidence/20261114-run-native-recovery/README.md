# Run native process recovery — 2026-11-14

**Result: all 26 functional scenarios passed. P0.3 and runtime release
eligibility remain open.**

The [sanitized report](report.json) identifies source snapshot
`432e6058ab21b335c2aea7bd633fe8e8df06bb68` (tree
`c8454906cf383454c359567635e84d87b027dbfa`), Go 1.26.11 and
Firecracker/jailer 1.7.0. The run used the selected `gregale-internal-test-1`
node in `gregale-prod`. It provides real KVM and reports Google virtualization;
this does not meet the repository's non-virtualized native release requirement.

The profile enables `native_process_recovery`, `native_launch_owner_records`
and `native_runtime_capture`, with private image, publication and snapshot
roots. A private mount namespace retains each fixture's native jail journal
across real SIGKILL/restart without changing the host journal. Capture barriers
exist only in metal-test binaries.

| Check | Verified behavior |
| --- | --- |
| Five vmmd capture crashes | Before boot, after pristine ACK, after pause, during memory publication and after completed capture; original guest retired, no catalog promotion, old unreferenced objects collected, healthy cold Run afterward |
| Schedd death during capture | Original capture retired; no old publication; a fresh worker captured and published a different template |
| Committed publication restart | vmmd and schedd restart preserved the original catalog key and authenticated digest; subsequent Run restored |
| Running customer work | vmmd and schedd restart each produced one guest execution, retained the original idempotent receipt, physically retired the guest and allowed a clean restore afterward |
| Native device ownership | Every customer wake had its original version-2 KVM-only receipt, KVM API 12 under the jail credentials, no TUN access or mount and closed producer inputs |
| Concurrent clones | Four distinct live process UIDs, jail roots, writable-drive inodes and capped cgroups; installed seccomp, separate scratch and RNG; cancelling one of two live clones preserved its peer |
| Warm/OOM and corruption | Real guest kernel OOM on cold and restored guests, clean recovery, and cold fallback for corrupt memory, VM state, drive and backing |
| Payload and usage | 27 customer Runs: 11 native recovery, nine template/clone and seven warm/OOM; one usage entry each, no retained payload rows, matching account counts; template accounts recorded zero Runs |
| Physical resources | Initial/final, all three fixtures before harness shutdown, and all five post-capture-crash leakchecks passed |

All ten native recovery cases, nine template/clone cases, seven warm/OOM cases
and required supporting suites passed with `-race` and `-count=1`; skipped
cases cannot produce this verdict. Deduplicated witnesses record 17 restores,
10 expected cold boots and 27 KVM-only customer-wake receipts. Two actual guest
OOM counters increased from zero to one. These are functional observations,
not startup-latency or fleet-capacity benchmarks.

Qualification found and fixed native networkless device preparation, a race
between image journal and anchor-directory scans, cached-channel reconnection
for fresh restore and stop delivery, and session acknowledgement before stop
completion. Ordinary capture authorities and networked device scopes retain
their existing rules. Template authorities and KVM-only scopes use explicit
new nested versions; compatible readers and rollback gates remain required.
Incomplete capture journals remain audit evidence and never grant replay or
catalog-publication authority.

The [failed-attempt summaries](attempts/summary.json) retain every preceding
failure and zero-leak cleanup, including the authority-fixture correction and
compiler scratch exhaustion before VM tests. The successful run separates Go
build temporaries (`GOTMPDIR`, root disk) from sandbox fixtures (scratch disk).
Completed private source copies were preserved as content/mode-verified
compressed archives; evidence and shared Go caches were retained.

The report requires final-source Linux lint for six changed packages, native
host-helper ownership regressions, complete coverage and independent on-node
SHA-256 verification. The verdict's 16 portable negative tests and shell
syntax/ShellCheck pass. Database schema, migration digest and clone registry
match the previously qualified template source; this step changes neither
SQL nor gRPC schema. Raw daemon logs remain on the node; only whitelisted
results and hashes are exported.

This qualifies bounded same-node production-code behavior for Node 24,
256 MiB RAM, 64 MiB scratch, local artifacts and fixture image admission.
It does not qualify production image provenance, other languages/profiles,
remote artifacts, host reboot, cross-node/CPU compatibility, older-reader
rollback, full billing or default quarantine-only unattended retirement.
No production service was enabled or deployed. Catalogued retired templates
still require a separate release-retention policy before deletion.

Reproduce with `FAAS_RUN_LIFECYCLE_COHORT=native-recovery` in the
[operator runbook](../../run-lifecycle-acceptance.md). The local reference
`refs/codex/run-native-recovery-qualification-20261114` retains the tested
source without changing the branch or index. See the
[native-template recovery ADR](../../../adr/20261114010000-run-native-template-recovery.md)
for authority, device setup and reader/rollback requirements.
