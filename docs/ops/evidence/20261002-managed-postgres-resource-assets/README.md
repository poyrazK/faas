# Managed PostgreSQL resource assets: internal KVM diagnostics

Date: 2026-10-02.

ADR-400 journals temporary materialization/reflink intent before creation and
file identity before copying. Image-bind intent precedes source chmod and target
creation; checkpoints record the placeholder and bound mount/source identities.
Live-owner cleanup refuses observed replacements, restores shared-source modes
only when safe, and retains failed cleanup or journal fsync for retry. A partial
bind keeps its writable clone until normal teardown finishes.

These records are provenance, not recovered lifecycle ownership or durable
fleet drain receipts. A fresh Manager cannot adopt or destroy a surviving guest
or apply its recovered failure reports. Complete jail/network namespace
incarnations, child TUN, loop/parent mounts, jail-local links/copies and immutable
snapshot publication remain outside this asset inventory. File/mount identifiers
can be reused; path checks are observations rather than an atomic compare-and-
unlink primitive. Verified restart cleanup, serving recovery, ownership epochs,
scheduler reconciliation and all-node drain proof remain pending.
Customer cutover activation stays disabled.

## Source and host

- Validation snapshot: `439c12019a36256135624d3fba68d0cf48776cfd`,
  tree `fe8b68c2f38df11b0775a3c72f35dd3ece50381b`.
- Parent: `ed4b8c8dd4be61f40f39e3bd44f24096ff495bc2` (ADR-399).
- Selected source archive: SHA-256
  `a5217bb61fae36e63289cd586d042869e810763a9f48211e5697cdcf080973bb`
  (13,446,695 bytes). `validation.json` lists its scope.
- All 6,246 workspace Go/module files match the snapshot (`source-match.log`).
  Subsequent changes are documentation/evidence only. The archive, runner and
  patch hashes passed before dispatch; the runner verifies the archive again.
  `source-hashes.sha256` derives from the exact archive and covers all 4,203
  selected Go/module files after execution.
- Project `gregale-prod`, zone `us-east1-b`, node `gregale-internal-test-1`;
  environment=test, fleet=excluded, purpose=internal-tests.
- x86_64 Linux `7.0.0-1011-gcp`, Go `1.25.13`, Firecracker/jailer `1.7.0`,
  n2-highmem-2 with nested virtualization enabled and `/dev/kvm` available.

These are nested-node diagnostics. Supported native x86_64 KVM acceptance and
filesystem power-loss qualification remain pending; reference SSD latency
acceptance is disabled. The runner requires root, the acceptance-host marker,
the shared acceptance lock and inactive Gregale VM/build/image/gateway services.
It copies shared module/build caches into an owned disk stage and writes only
private caches. The transient unit provides a four-GiB build TMPDIR, one-GiB jail
tmpfs and MemoryMax=10G. GOMAXPROCS=1, GOGC=50 and GOMEMLIMIT=2GiB bound compilation.
Exact-source static guest-init and immutable two-drive busybox HTTP fixtures
preserve the stock metal boot shape. The stage/run identifier comes from the
initial preparation; the final source hash above and checked archive identify
all executed source. `runner.sh` and `dispatch-command.txt` retain the invocation.

## Results

| Selected metal checks | Passed | Skipped | Failed |
| --- | ---: | ---: | ---: |
| Top-level tests | 31 | 0 | 0 |
| Including subtests | 78 | 0 | 0 |

The second row includes the first. Real bind lifecycle passed in 0.02 s,
foreign mount/target refusal through a symlinked jail path in 0.01 s, and bind
checkpoint failures in 0.03 s.
The surviving-guest journal regression passed in 2.09 s, including reopened
image-bind provenance and rejection of unowned teardown. Metal fcvm completed
in 15.966 s and cmd/vmmd in 1.165 s. Bounded Linux race regressions passed for
fcvm (1.190 s) and cmd/vmmd (1.307 s).

All three exit files record zero, and changed-code Linux lint reports zero
issues. Three in-run leak checks passed while the shared acceptance lock was
held; no extra host-wide leak check ran after releasing it.

The completed unit was collected: LoadState=not-found, ActiveState=inactive,
SubState=dead, Result=success and ExecMainStatus=0. The explicit exit files and
completed run log independently record success. The source manifest hash and
all 4,203 selected Go/module files matched after execution
(`source-runtime-match.log`). The complete results archive was downloaded with
matching remote/local SHA-256 before cleanup (`download-verification.log`).

Both transient units are inactive. Canary cleanup recorded state=completed and
removed the owned disk stage (`cleanup.log`). Other jobs, services and shared
caches were preserved.

The first source-pinned run also passed 31 top-level/78 total tests, Linux lint
and three leak checks. Final review added resolved jail-parent paths and a
symlink case to the foreign-mount regression. The final pinned run repeated all
selected checks. `initial-*` files retain the earlier completed run; final
results above refer exclusively to the snapshot listed here. The second run
reused private caches and unpacked into `source-final`; earlier outputs were
moved aside before dispatch to prevent stale exit files from reporting success.

## Local validation

Complete final macOS race suites passed for fcvm (19.671 s) and cmd/vmmd
(10.189 s). Changed-code lint reports zero issues. Generated units, text encoding,
shell quoting, sealed-env scope, ADR uniqueness and changed core-test citations
passed. macOS LC_DYSYMTAB linker warnings were nonfatal and are retained.
No Go dependency, database migration, customer quota, RPC or environment variable
was introduced. Journal record/path/count bounds are parser/storage limits.

The first local lint invocation found a missing shared Go cache entry. A retry
with a private build cache reused the same analysis cache and the analyzer
panicked. A clean private analysis cache passed with zero issues. No runtime
source was changed to suppress these tool failures. An initial per-file source
comparison was stopped and replaced by a batched comparison of all Go/module
blob hashes; `source-match.log` records the successful final comparison.

## Diagnostic scope

`make test-metal` selects `./pkg/fcvm ./cmd/vmmd` with `-race -count=1`,
`-timeout=20m -v` and the explicit RUN_REGEX in `runner.sh`. It covers ADR-400
ordered intent/checkpoint/reopen, failed fsync and replacement guards, actual
read-only binds and shared permission restoration, plus journal/quarantine,
confirmed teardown and failure-report ownership dependencies. Unselected tests
and the unfiltered native release suite are outside scope. Bounded Linux race
regressions supplement the complete macOS suites; no full Linux suite was run.

Tests keep the original fixture owner for physical cleanup. Reopening journal
storage does not reconstruct mount/watchdog ownership in the fresh Manager.
These are not full daemon-crash, recovered serving or power-loss experiments.
The journal fixtures reside in TMPDIR tmpfs; ordered fsync/failure handling does
not qualify persistent filesystems for power-loss durability.

Committed logs normalize carriage returns and trailing whitespace/blank lines.
`RAW_SHA256SUMS` records original bytes in the local task checks directory;
`SHA256SUMS` covers committed evidence. `change-patch.json` contains the exact
UTF-8 patch lines and checksum; joining them recreates the runtime patch.
The reconstructed final Go patch passes a reverse application check. A
credential-marker scan of committed evidence found no matches; the runtime patch
was excluded because it contains synthetic regression fixtures.
