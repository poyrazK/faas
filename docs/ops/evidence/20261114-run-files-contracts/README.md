# Stored Run files and standalone contracts — functional verification

**Result: the bounded Node 24 real-VM journey passed. The preview writer remains
disabled by default; native production qualification remains open.**

The [verification record](verification.json) binds base snapshot
`432e6058ab21b335c2aea7bd633fe8e8df06bb68` plus 198 uploaded files, with delta-manifest SHA-256
`528cfc964eba6855df9c54c4fcf154dfd47e9dbc79f227749c32f51e2ec347a4`. The [worktree check](worktree-check.json)
compares all 50,950 non-documentation files against that source and finds no
mismatches or extra source files. Documentation status and rollout instructions
were updated after qualification; the manifest records the tested snapshot.
An independent [postflight check](postflight.json) rehashed all uploaded files,
the four functional logs and 19 retained artifacts after teardown.

On `gregale-internal-test-1` in `gregale-prod`, Go 1.26.11 Linux/amd64:

| Check | Result |
| --- | --- |
| File, contract and interpreter tests | 63 passing results |
| State regressions | 21 passing results |
| Scheduler and VM transport | 140 passing results |
| API and transfer handlers | 255 passing results |
| Required PostgreSQL file lifecycle tests | 4 passing results |
| CLI regressions | 984 passing results |
| Public routing | 111 passing results |
| File/contract race checks | 29 passing results |
| Changed-package lint | Six groups passed |
| Real-VM journey | All eight required scenarios passed with race instrumentation |
| Initial/final physical leak checks | Both exited zero; pre-teardown cleanup also passed |

The final matrix records 1,607 passing test results, including subtests.
Optional host interpreter/data-profile and PostgreSQL tests are skipped in the
portable phases where their fixtures are deliberately absent; the dedicated
PostgreSQL phase requires all four file tests with no skips. The real-VM
journey permits no skipped scenario. [Portable checks](portable-checks.json)
retain additional local regression results from this implementation session;
the Linux matrix is the source-bound final qualification.

The VM journey uses a 12 MiB input and stored output through apid's real
customer API listener, PostgreSQL, schedd, vmmd, the guest interpreter and the
CLI. Distinct CLI input bytes force an actual upload. It verifies missing-input
refusal before admission, immutable upload identity and key-family isolation,
a cold data round trip with a valid result contract, two concurrent restored
clones with independent input copies, invalid-contract receipt preservation
and replay, CLI upload/download checksums, revocation with apid restart, and
single usage/payload erasure with physical retirement. Public v2 routing and
tenant-host boundaries have separate reverse-proxy unit coverage.

The first functional attempt stopped on an incorrect test assertion that did
not unwrap the API error carrier. The final journey also corrects a newline
expectation and prevents the CLI case from reusing an already uploaded input.
The cold data journey also found an incorrect dependency on an outbound
integration lease in file identity. The final source permits live plain-Run
file access while retaining outbound refusal and post-retirement fencing.
Raw attempt logs remain private on the node. The source-bound unit run exposed
a test-environment issue: operator umask 077 makes root-created bundle
subdirectories unreadable to the guest UID. The unchanged Python test was
reproduced failing under 077 and passing under 022; the complete interpreter
phase then passed under 022. Both diagnostic log hashes are retained. No test
was removed or skipped to resolve it.

Passing lint groups were reused only after their complete target-source and
lint-configuration hashes matched. The changed runtime and VM-test targets
were rerun after the final corrections. The record preserves both source
manifests and log hashes. Private daemon logs, credentials and payloads
were not exported.

This is functional evidence on a Google-virtualized KVM fixture, using the
local artifact backend and Node 24 standard profile at 256 MiB memory / 64 MiB
scratch. It grants no native release eligibility, production image provenance,
other-language/profile qualification, deployed CDN journey or billing-rollup
qualification. Existing native lifecycle and release-preflight gates remain.

Apply the additive schema and compatible runtime/API/proxy readers before
setting `FAAS_EXECUTION_V2_ENABLED=1`. Disable new admissions before rollback,
drain extended Runs and preserve compatible file readers, keys, storage and GC
until retained files are gone. See the [usage guide](../../../executions.md),
[decision](../../../adr/20261114030000-run-stored-files-and-contracts.md), and
[acceptance lane](../../run-lifecycle-acceptance.md#stored-files-and-standalone-contracts-cohort).
