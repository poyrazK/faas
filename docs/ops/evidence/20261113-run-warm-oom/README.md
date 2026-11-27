# Run warm restore and OOM functional acceptance — 2026-11-13

**Result: passed for Node 24 standard. P0.3 and runtime release eligibility remain open.**

The [sanitized report](report.json) records exact-source snapshot
`7c4f28da3cd48f819120a1202f0f3f492a8d9338`, Go 1.26.11 and
Firecracker/jailer 1.7.0 on the same `gregale-internal-test-1` node in
`gregale-prod`. This is real-KVM functional evidence on a host reporting
Google virtualization; it does not satisfy the repository's non-virtualized
native-host requirement.

| Check | Result |
| --- | --- |
| Pristine capture before any Execute RPC | Passed; 256 MiB memory, 13,716-byte VM state, coupled private drive |
| Repeated snapshot restores | Passed; separate payloads and scratch, fresh random bytes, corrected clock, UID 1000, loopback only |
| Actual guest kernel OOM, cold and restored | Both passed; `oom_kill` increased from 0 to 1, receipt `out_of_memory`, positive usage |
| Clean restore after OOM | Passed |
| Corrupt memory and corrupt VM state | Each rejected; cold fallback returned the expected result |
| Payload erasure and usage ledger | Seven Runs; no retained payload rows, one usage entry each, matching monthly Run count |
| Physical resource checks | Initial, before harness shutdown, and final checks passed |
| Guest resume and OOM attribution regressions | Passed with race instrumentation |

All seven cohort scenarios and both auxiliary packages passed with `-race`
and `-count=1`. Native wake logs prove four restores and three cold boots;
warm cases fail if the scheduler silently cold-boots. Ordinary SIGKILL,
ordinary exits and unavailable/regressing kernel counters do not qualify as
OOM. The guest rejects resume after accepting caller bytes, and missing
resume entropy still fails closed.

The trusted test producer captures a new guest before any caller payload or
Execute RPC, verifies the complete capture and registers its durable catalog
entry. It retains the native frozen drive and backing namespace. This is not
qualification of an automated production template service, concurrent clones,
startup latency, other languages/profiles, production image provenance,
default recovery, cross-node recovery or full billing.

Raw logs remain on the test node. Automatic approval review rejected their
export because they include private topology and daemon diagnostics. A
separate on-node check verified their hashes and complete coverage, then
exported only the report's whitelisted counters, artifact hashes and results.
The report also records Linux changed-code lint and portable runner checks.

Earlier attempts stopped at a privileged test-helper permission error, a
missing private snapshot directory, and a probe JavaScript syntax error.
Each retained its failed verdict and exited both physical leakchecks cleanly;
none counts as passing coverage.

Reproduce with the warm/OOM cohort in the
[operator runbook](../../run-lifecycle-acceptance.md). The local reference
`refs/codex/run-warm-oom-qualification-20261113` retains the tested source
without changing the branch or index. The
[earlier baseline](../20261113-run-lifecycle/README.md) covers cold source
lifecycle and OCI journeys separately.
