# Managed PostgreSQL resource intent journal: internal KVM diagnostics

Date: 2026-10-02.

ADR-399 commits vmmd lease intent before new network/artifact/guest resources
and checkpoints the guest's kernel boot ID, PID and process start ticks before
boot returns. Confirmed physical cleanup precedes journal retirement; durable
retirement precedes allocator release. Restart inventory reserves every record,
including intent with no observable resources, and reports process provenance.

A record or matching process does not reconstruct lifecycle ownership, authorize
recovered failure-report application or let a replacement Manager destroy a
guest. Mount/artifact/namespace incarnation journaling, verified restart cleanup,
serving recovery, ownership epochs and fleet drain proof remain pending.
Customer cutover activation stays disabled.

## Source and host

- Validation snapshot: `4e4f5a64516d7b448f2cd65266b97983d7108408`,
  tree `118b5a30986d40a8b828eaf2f6c824713abd18e5`.
- Parent: `18183518527698e08439fefcdff50bee16f56e04` (ADR-398).
- Selected source archive: SHA-256
  `9b3caf60a4dd2b94e520abefcfa62e219b81c0479e1e1a57b125349714a5390d`
  (13,439,166 bytes). `validation.json` lists selected paths, including
  the complete guest source needed to build guest-init.
- All workspace Go/module files match the snapshot (`source-match.log`).
  Subsequent changes add documentation and evidence only. Archive, runner and
  patch checksums passed before dispatch; the runner verifies the archive again.
  `source-hashes.sha256` derives from the exact archive and checks all 4,197
  selected Go/module files again after execution.
- Project `gregale-prod`, zone `us-east1-b`, node `gregale-internal-test-1`;
  environment=test, fleet=excluded, purpose=internal-tests.
- x86_64 Linux `7.0.0-1011-gcp`, Go `1.25.13`, Firecracker/jailer `1.7.0`,
  n2-highmem-2 with nested virtualization enabled and `/dev/kvm` available.

These are nested-node diagnostics, not supported native x86_64 KVM acceptance.
Reference SSD latency acceptance is disabled. The runner requires root, the
acceptance-host marker, the shared acceptance lock and inactive Gregale
VM/build/image/gateway services. It uses an owned six-GiB stage tmpfs with copied
module and private build/lint caches. The transient unit provides a four-GiB
build TMPDIR and one-GiB jail tmpfs with MemoryMax=10G. GOMAXPROCS=1, GOGC=50 and
GOMEMLIMIT=2GiB bound compilation. Exact-source static guest-init and immutable
two-drive busybox HTTP fixtures preserve the stock metal boot shape.
`runner.sh` and `dispatch-command.txt` preserve the invocation.

## Results

| Selected metal checks | Passed | Skipped | Failed |
| --- | ---: | ---: | ---: |
| Top-level tests | 21 | 0 | 0 |
| Including subtests | 55 | 0 | 0 |

The second row includes the first; do not add them. The new surviving-guest
journal regression passed in 2.02 s. The existing real-guest quarantine checks
passed UUID (1.97 s), builder-prefixed (1.98 s) and compact IDs (1.99 s).
Metal fcvm completed in 15.329 s and cmd/vmmd in 1.152 s. Linux portable race
regressions passed for fcvm (1.180 s) and cmd/vmmd (1.402 s).
All three exit files record zero. Changed-code Linux lint reports zero issues.
All three in-run leak checks passed while the shared acceptance lock was held;
no extra host-wide leak check ran after releasing it.

The completed transient unit was collected: LoadState=not-found,
ActiveState=inactive, SubState=dead, Result=success and ExecMainStatus=0.
The explicit exit files and completed run log independently record success.
All 4,197 selected Go/module files match the archive after execution
(`source-runtime-match.log`). The complete results archive was downloaded with
matching remote/local SHA-256 before cleanup (`download-verification.log`).

Owned stage storage was unmounted; canary cleanup recorded state=completed
and removed the stage directory. The unit is collected and inactive/dead
(`cleanup.log`). Other jobs, services and caches were preserved.

## Local validation

The complete final macOS race suites passed for fcvm (16.967 s), cmd/vmmd
(10.631 s), daemonunitspec (21.945 s) and daemonunit (1.539 s).
Changed-code lint reports zero issues. Generated service units, text encoding,
shell quoting, sealed-env scope, ADR uniqueness and new core-test citations
passed. macOS LC_DYSYMTAB linker warnings were nonfatal and are retained.
No Go dependency, database migration, quota, RPC or environment variable was
introduced. `resource_journal_dir` is a TOML configuration field.

Portable journal regressions cover exclusive access, concurrent persistence and
reopen, intent-before-network ordering, failed fsync and retirement retry,
mutable CPU policy, intent-only quarantine, reused PID/boot/UID/instance
mismatches and corrupt storage. Execution commands, environment values and lease
tokens are excluded from journal records.

Initial local development runs exposed private-directory fixture permissions
and incomplete job fixtures; they were corrected. The first lint run found an
error comparison, corrected to errors.Is. Another invocation encountered the
linter's parallel-run lock; the final invocation used a private cache and
allow-parallel-runners and passed. These are not final test results.

## Diagnostic scope

`make test-metal` selects `./pkg/fcvm ./cmd/vmmd` with `-race -count=1`,
`-timeout=20m -v` and the explicit RUN_REGEX in `runner.sh`. The selection covers
ADR-399 journal persistence/reopen, ADR-398 restart inventory and real guest ID
forms, confirmed teardown, failure outbox and recovered report ownership.
Unselected tests and the unfiltered native suite are outside scope. The bounded
Linux portable selection supplements the complete passing macOS suites; its
size follows ADR-398's earlier unit-local full-suite memory failure. No full
Linux suite was attempted for this slice.

The new metal regression boots a real guest and reopens its journal storage,
then verifies process provenance in a fresh Manager. It rejects unowned teardown
and boots another guest on a different slot/UID while the original survives.
Only the original fixture Manager retains watchdog/mount/artifact bookkeeping
and retires the survivor's record during cleanup. This is not a full daemon
crash, recovered serving or power-loss experiment. Journal fixtures live in
TMPDIR tmpfs; filesystem power-loss durability qualification remains pending.

Committed logs normalize carriage returns and trailing whitespace/blank lines.
`RAW_SHA256SUMS` records original bytes retained in the local task checks
directory; `SHA256SUMS` covers committed evidence. `change-patch.json` stores the
exact runtime patch as UTF-8 lines with its checksum; joining those lines
recreates the runtime patch. The reconstructed Go patch passes a reverse
application check against the final source. A credential-marker scan of the
committed logs found no matches.
