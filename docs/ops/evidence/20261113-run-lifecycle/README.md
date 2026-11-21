# Run lifecycle functional acceptance — 2026-11-13

**Result: passed within the scope below. P0.3 and runtime release eligibility remain open.**

The [sanitized report](report.json) records exact-source snapshot
`8105ab70a1a14f06d2a71b4694d26c79efeea064`, Go 1.26.11, Firecracker/jailer
1.7.0, and the selected `gregale-internal-test-1` node in `gregale-prod`.
The host reports Google virtualization. This is real-KVM functional evidence;
it does not satisfy `CLAUDE.md`'s native-host acceptance requirement.

| Check | Result |
| --- | --- |
| Node 24 standard-profile source cold boots | All 11 scenarios passed; package 92.553 seconds |
| OCI caching, commands, exit codes and cancellation after claim | Passed |
| OCI USER, WORKDIR, ENV, PATH and overrides | Passed |
| Interactive pipes, PTY and disconnect cancellation after guest output | Passed |
| OCI package | Passed; 288.837 seconds |
| Linux liveness/subscription regressions | 9 test nodes passed with race instrumentation |
| Image-materialization regressions | 8 tests passed with race instrumentation |
| Initial and final physical resource checks | Both exited zero |
| Source physical cleanup before harness shutdown | Passed |
| Linux changed-code lint | Zero new findings in the tested runtime/harness diff |
| Portable verdict checker, Bash syntax and ShellCheck | Passed; 6 verdict-checker tests |

These package durations are test execution times, not customer startup latency
benchmarks. All selected test packages ran with `-race` and `-count=1`.
The verdict rejects missing cases, skips, failures and nonzero leakcheck exits.
Downloaded core logs were checked against their recorded hashes, and coverage
was independently rechecked locally. Raw logs remain private because they
contain internal topology and daemon diagnostics; the report retains hashes.
Linux lint uses the pinned golangci-lint tool and the exact source diff, with
the package scope and patch hash recorded in the report.

The run used private artifact stores, fresh test keys, an exact-source guest
init in a copied Node 24 base, and an OCI minimal-userland fixture staged by
the image producer. The builder startup fixture has inert tools; no source
builder is qualified here. Source scan admission is a test fixture. This is
not production image-provenance or vulnerability-scan qualification.

Two runtime corrections are covered: guest failures containing protocol JSON
`null` now retain their correct receipt failure codes, and image preparation
stages the minimal boot base before publishing an OCI job as ready. The latter
failure is preserved in [the earlier attempt](attempts/0ea8f5b6.json).
The harness also detects unreaped Linux exits and waits for `/readyz` as well
as a current database subscription.

Source restart checks enable `adoption_record` and `adopt_running_vms` with
version-7 ownership journals. The default quarantine-only configuration and
the separate `native_process_recovery` driver remain unqualified. Adoption
requires its documented reader-before-writer and rollback rollout gates.

The [subsequent warm/OOM cohort](../20261113-run-warm-oom/README.md) covers
sanitized restore, corruption fallback and actual OOM for Node 24 standard.
Other runtime/profile cohorts, deployed cross-node/reconnect behavior,
production image checks and full billing remain open. The schema-isolated lane
verifies one execution usage entry per source Run; meterd's missing
public-rollup warnings do not constitute invoice validation.

Reproduce using the [operator runbook](../../run-lifecycle-acceptance.md).
The local reference `refs/codex/run-lifecycle-qualification-20261113` retains
the tested source object without changing the working branch or index.
