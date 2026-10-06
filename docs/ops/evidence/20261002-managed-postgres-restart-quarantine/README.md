# Managed PostgreSQL restart resource quarantine: internal KVM diagnostics

Date: 2026-10-02.

ADR-398 prevents a fresh vmmd allocator from reusing identities observed on
surviving guests. Linux startup inventories guest processes, links, namespaces
and jails before prepared-network allocation or RPC service. Observed instance
IDs reject boot, resumable operations and teardown; quarantine does not invent
lifecycle ownership or authorize ADR-397 report replay.

These are nested-node diagnostics, not supported native x86_64 KVM acceptance.
The real-guest regression retains the original Manager solely for fixture
cleanup and constructs a fresh Manager/VMM against surviving resources. It does
not simulate a full daemon crash, recover serving/routing/watchdogs, reconstruct
scheduler RAM accounting or establish fleet drain proof. Customer cutover
activation remains disabled.

## Source and host

- Validation snapshot: `4d700a7176e697e8a76ed4b92a45da79faee8d7d`,
  tree `558c832e2ccebba13b02646a9543fee7634f177c`.
- Parent: `1f6b59838b7119ec2770a073a7a2f24c699e96b9` (ADR-397).
- One selected source archive: SHA-256
  `43eb47623b2cb330d339d882ab89285e3a597bcc30bf6b993d8144c53feef6e9`
  (13,430,787 bytes). Selected paths are in `validation.json`; the archive
  contains the complete guest source required to build guest-init.
- All 6,235 Go/module files match this snapshot (`source-match.log`). Subsequent
  edits add documentation and evidence only. Archive, runner and patch checksums
  passed before dispatch; the runner checks the archive again before extraction.
- Project `gregale-prod`, zone `us-east1-b`, node `gregale-internal-test-1`;
  environment=test, fleet=excluded, purpose=internal-tests.
- x86_64 Linux `7.0.0-1011-gcp`, `/dev/kvm`, Go `1.25.13`,
  Firecracker/jailer `1.7.0`, n2-highmem-2; nested virtualization enabled.
- Reference SSD latency acceptance disabled. No production deployment,
  customer database binding change or cutover activation.

The runner requires root, the acceptance-host marker, the shared acceptance
lock and inactive Gregale VM/build/image/gateway services. Owned stage storage
uses a six-GiB tmpfs with copied module and private build/lint caches. The
transient unit provides a four-GiB build TMPDIR and one-GiB jail filesystem,
with MemoryMax=10G. `runner.sh` and `dispatch-command.txt` preserve the invocation.
The final bounded run uses GOMAXPROCS=1, GOGC=50 and GOMEMLIMIT=2GiB. It builds
exact-source static guest-init and immutable two-drive busybox HTTP fixtures.
Transfers completed before dispatch. It waited for another test unit's shared
lock; other jobs, services and caches were preserved.

## Results and scope

| Selected metal checks | Passed | Skipped | Failed |
| --- | ---: | ---: | ---: |
| Top-level tests | 13 | 0 | 0 |
| Including subtests | 29 | 0 | 0 |

The second row includes the first; do not add them. The real-guest quarantine
regression passed all three ID forms in 5.93 s (UUID 1.99 s, builder ID 1.99 s,
compact ID 1.95 s). fcvm completed in 13.205 s and cmd/vmmd in 1.149 s;
metal.exit=0. Linux changed-code lint reports zero issues and linux-lint.exit=0.
All three in-run leak checks passed while the shared acceptance lock was held.
No extra host-wide leak check was run after releasing that lock.

`make test-metal` selects `./pkg/fcvm ./cmd/vmmd` with `-race -count=1`,
`-timeout=20m -v` and the explicit RUN_REGEX in `runner.sh`. The selection covers
restart inventory, retained teardown ownership, failure outbox and recovered
report ownership. The new real-guest regression checks UUID, builder-prefixed
and compact IDs. It refuses an unowned stop/boot, then boots a second guest with
distinct slot, jail UID, host IP and vsock CID while the original guest survives.
Unselected tests and the unfiltered native acceptance suite are outside scope.

The bounded Linux portable race regressions passed for fcvm (1.148 s) and
cmd/vmmd (1.126 s), including startup inventory and readiness checks.
`portable-linux.exit` records zero.

The completion poll observed inactive/dead, Result=success and ExecMainStatus=0.
The transient unit had been collected (LoadState=not-found); the three explicit
exit files and completed run log independently record success. Complete logs
were downloaded before stage cleanup.

The downloaded archive's SHA-256 matched the node's checksum. Owned stage
storage was unmounted; canary cleanup recorded state=completed and removed its
directory. The expired initial stage is also absent. The final unit is collected
and inactive/dead; this task's three failed unit states were reset. Other jobs,
services and caches were preserved (`cleanup.log`).

## Portable checks and earlier attempts

The complete final macOS race suite passed for fcvm (16.701 s) and cmd/vmmd
(10.019 s). Changed-code lint reports zero issues. Text encoding, shell quoting,
sealed-env scope, ADR uniqueness and new core-test citations passed. macOS
LC_DYSYMTAB linker warnings were nonfatal and are retained. No new Go dependency,
database migration, quota, RPC or environment variable was introduced.

Earlier attempts are retained separately and are not counted as final evidence:

1. Initial local lint found an ineffectual slot assignment; the code was repaired.
   The initial UUID-only internal selection passed its selected tests and three
   leak checks, then lint failed because the transient unit lacked a writable
   cache environment. An explicit lint cache fixed that invocation. Only partial
   checkpoint output survives: the stage's six-hour active lease expired before
   complete raw logs were downloaded. `initial-metal-checkpoint.txt`,
   `initial-lint-completion.txt`, `stage-expiry.txt` and `expired-dispatch.log`
   preserve this limitation; the expired checksum dispatch did not run tests.
2. Inventory was expanded for actual builder-prefixed and compact internal IDs.
   A local rerun hit disk exhaustion while linking. The Linux full portable run
   exposed three existing readiness-test failures: its host-check helper included
   a fresh, unbound gRPC signal and always reported false on a healthy KVM host.
   The test helper now marks that signal bound before evaluating host checks;
   production readiness is unchanged. `local-disk-failure.log` and
   `linux-readiness-failure.log` retain these failures.
3. The final pinned source's full Linux fcvm race run exceeded its owned unit's
   10-GiB memory limit before metal checks. The kernel recorded a unit-local
   memory-cgroup OOM and killed fcvm.test at 7,941,772 KiB anonymous RSS. This is
   not a passing full Linux suite. `linux-full-oom.txt`, `oom-cause.txt` and
   `oom-run.log` preserve it. The final bounded Linux regressions supplement the
   passing complete macOS suite; the source is unchanged between these runs.

## Recovery limits

Quarantine holds slots for the Manager's lifetime, even if an existing orphan
sweep subsequently removes a resource. An observation never authorizes Release
or recovered failure-report application. Reclaiming held capacity requires a
fresh startup observing absence until durable resource journaling,
process-incarnation verification, confirmed cleanup and scheduler reconciliation
are implemented. Quarantine itself does not kill or remove resources; the
existing durable-state sweeps retain their separate behavior. Native lifecycle
acceptance, serving recovery, same-node ownership epochs and fleet cutover proof
remain pending.

Committed logs normalize carriage returns and trailing whitespace/blank lines.
`SHA256SUMS` covers committed evidence; `RAW_SHA256SUMS` records original bytes
retained in the local task checks directory. A credential-marker scan of the
downloaded and committed logs found no matches. The committed Go diff matches
the validation snapshot. `change-patch.json` stores the exact runtime patch as
UTF-8 lines with its checksum; joining its lines recreates `change-final.patch`.
The recreated patch passes a reverse application check against those Go files.
