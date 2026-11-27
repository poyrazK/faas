# Run production templates and concurrent clones — 2026-11-13

**Result: passed for the local Node 24 standard producer and four same-node
clones. P0.3 and runtime release eligibility remain open.**

The [sanitized report](report.json) records exact-source snapshot
`3fc785dde80e849430b8d4ba7f10dcc3001785c5` (tree
`ab017607cb8ca613a07124b6f3d2d38242e86103`), Go 1.26.11 and
Firecracker/jailer 1.7.0 on `gregale-internal-test-1` in `gregale-prod`.
The host provides real KVM but reports Google virtualization, so it does not
satisfy the repository's non-virtualized native-host release requirement.

| Check | Result |
| --- | --- |
| Production capture and publication | Schedd's opt-in worker started with an empty catalog; vmmd booted a fresh payload-free guest, required its pristine resume ACK, captured and physically destroyed it before publication |
| Private immutable artifacts | All four objects were root-owned, unavailable in shared storage, authenticated together and protected against overwrite or namespace escape |
| Restart and failed capture | Scheduler restart reused the same durable publication; a wrong kernel pin left no row or live VM |
| Publication uncertainty and races | Partial capture, lost commit acknowledgement, unknown commit and competing publishers passed race-instrumented regressions |
| Catalog-aware GC | Ready and retired catalog rows remained protected; only old unreferenced captures were reclaimed through vmmd |
| Four live restored clones | Four distinct actual process UIDs, jail roots, writable-drive inodes and memory-capped cgroups; installed seccomp, separate scratch and RNG outputs, loopback-only guest interfaces |
| Concurrent cancellation | Both clones restored and ran together; cancelling one preserved its successful peer |
| Corruption fallback | Memory, VM state, drive and backing corruption each selected cold boot and returned the expected result |
| Warm/OOM regression | All seven scenarios passed against the production publisher; both real guest OOM counters increased from 0 to 1, with recovery afterward |
| Usage and payload cleanup | Nine template-cohort customer Runs plus seven warm/OOM Runs; one usage entry each, no retained payload rows, matching monthly counts; the template account had zero Runs |
| Physical cleanup | Initial, both fixtures before harness shutdown, and final leakchecks passed |

All nine template/clone scenarios, all seven warm/OOM scenarios and seven
supporting packages passed with `-race` and `-count=1`. Native wake witnesses,
deduplicated by Run ID, prove 11 restores and five expected cold boots. The
immutable template digest remained unchanged after concurrent execution.

The first [attempt](attempts/68df2bef.json) failed because the probe read an
unpopulated `LeaseUid` field from Stats. Native wake evidence already showed
four distinct UIDs. The corrected probe reads the actual Firecracker process
credentials and checks them against the native wake record. The failed
attempt retained its failed verdict and passed initial/final leakchecks.

The report records Linux changed-code lint, fresh private PostgreSQL schema
regeneration and independent on-node coverage/hash verification. The canonical SQL constraint order was normalized before the final functional
run. Its hash matches the fresh dump and sqlc regeneration is deterministic. Raw logs
remain on the node; only whitelisted results and hashes were exported.

A combined lint job on the earlier candidate reached its 8 GiB memory ceiling and was stopped
without a completed verdict. Its resource evidence is retained. The same
eight-package scope is checked separately with tests enabled, one analyzer
worker and a 12 GiB supervisor limit; the report requires every package to
exit zero. The earlier candidate's only lint finding was a Boolean-expression simplification;
that correction precedes the final native run. The serial execution changes
the check's resource profile, not its scope.

This is bounded functional qualification of production code with fixture
image admission. It does not qualify production image provenance, remote
storage, other language/profile cohorts, cross-node or CPU compatibility,
fleet concurrency, startup latency, full billing, default quarantine recovery
or the full native-process-recovery producer. Capture remains opt-in and was
not enabled in production services. Retired catalog artifacts still require
a separate release-retention policy before physical deletion.

Reproduce with `FAAS_RUN_LIFECYCLE_COHORT=template-clones` in the
[operator runbook](../../run-lifecycle-acceptance.md). The local reference
`refs/codex/run-template-clones-qualification-20261113` retains the tested
source without changing the branch or index. See the
[capture/publication ADR](../../../adr/20261113180000-run-template-capture-publication.md)
for rollout gates and the [earlier warm/OOM report](../20261113-run-warm-oom/README.md)
for the historical operator fixture.
