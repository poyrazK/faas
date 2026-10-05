# Prior-boot spare retirement diagnostics — 2026-10-03

These are internal GCP **nested virtualization diagnostics**, not supported native
x86_64 KVM acceptance. Customer cutover stays disabled. The source implements
[ADR-404](../../../adr/404-managed-postgres-prior-boot-spare-retirement.md).

## Source and host

- Validation snapshot: `50856490632dc16d61d3c8fd01ae4b8c58980def`; parent `7d01f9106a41f93de0d83a4ed22e59746594dcd0`.
- Source tree: `008470099d2bf0205d064818c7391b89a2f1f07e`.
- Archive SHA-256: `cc93bea0aecd545b00b67df41219206ad0ca527e2e9d81318fdea9c7157285b4`.
- Change patch SHA-256: `b61c627906a3c3b67b992f6176a9fdd4cb2f2b4b6383c16aa3316f7237ae325d`.
- Host: `gregale-prod / us-east1-b / gregale-internal-test-1`, nested-enabled
  `n2-highmem-2`, Intel Cascade Lake, Go 1.25.13, Firecracker 1.7.0.
- Final unit: `gregale-managed-pg-restart-reclaim-adc1b1d8-retry`; invocation `971d10c331a04144bce3120c36838b92`.

[validation.json](validation.json) pins the source and runner inputs.
[source-hashes.sha256](source-hashes.sha256) and
[source-runtime-match.log](source-runtime-match.log) cover 4233
selected Go/module files before and after Linux execution.
[final-source-match.log](final-source-match.log) confirms all
6,264 workspace Go/module files still match the immutable
snapshot after macOS tests. Initial verification used SHA-256; the final whole
workspace check compares Git blob hashes of the actual file bytes.

## Results

The bounded `make test-metal` selection passed **79 top-level
tests (255 including subtests)** with zero failures and skips.
[metal.log](metal.log) contains five prior-boot cases: absent resources, a real
foreign namespace, a real foreign veth, a real dummy link and an unrelated process
running under the reserved jail UID. Only the absent case retires the record and
makes its slot available. Collision cases retain their reservation and preserve
the physical identities/process. Prior-boot provenance is injected into the
journal; these tests do not perform an actual host reboot.

The existing five separate-process SIGKILL checkpoints (spare, transfer intent,
dual aliases, moved alias and guest intent) continue to quarantine same-boot
resources. The guest-intent crash fixture launches no VMM; other selected tests
exercise real Firecracker guest/process and image-bind provenance.

Full fcvm/vmmd race suites passed on Linux and macOS. Linux vmmd portable tests
run as `nobody` from the package working directory. Portable regressions cover
retirement fsync before allocation, replay, incomplete/mixed-boot records,
transfers/adopted guests, all reserved network names, dangling markers, jails,
unrelated/partial UID holders, invalid inventory, cancellation and fsync retry.
All final test/lint exit files are zero; [unit-final.txt](unit-final.txt) confirms
the final transient unit is inactive; [unit-journal.log](unit-journal.log) confirms
successful completion before systemd unloaded it. Changed-code lint, egress
render parity, kernel nft syntax, bridge-name and generated deployment checks
passed. Static policy and test-citation checks passed. Image validation skipped
because Packer is absent; this change edits no image templates.

Three lifecycle leak checks passed before, after and on exit from the final run,
all while holding the shared acceptance lock. The final run uses private Go and
lint caches, tmpfs jail/temporary paths and a reduced compiler memory target.

## Preserved failures and cleanup

The initial Linux unit was OOM-killed during compilation, before any portable or
metal result. [initial-oom-unit-final.txt](initial-oom-unit-final.txt) and
[oom-diagnostic.log](oom-diagnostic.log) preserve that failure. The retry lowers
`GOMEMLIMIT` from 2 GiB to 768 MiB and `GOGC` from 50 to 20; it runs exactly the
same source snapshot. That failed unit is not counted as a successful gate.
Its interrupted exit trap supplied no final leak check; the retry's initial
lock-held leak check passed before proceeding.

[initial-compile.log](initial-compile.log) records an early helper return-type
mismatch corrected before the final snapshot. The first macOS suite run then hit
Unix socket path-length limits from the chosen long temporary directory; its
logs are preserved as `long-tmp-failed-macos.*`. The final run uses a short,
task-specific temporary path and passed without source changes.

[download-verification.log](download-verification.log) verifies the complete
result archive SHA-256 before cleanup. An initial download hit local disk
exhaustion; only this run's reproducible source archive/private index were removed,
and the download retry verified successfully. [initial-download.log](initial-download.log)
preserves that transfer failure. [cleanup.log](cleanup.log) confirms both
owned units are inactive/unloaded and the registered stage was removed after
collecting evidence. Private macOS caches and temporary directories were removed;
shared caches and other tasks were untouched. Registry kind `native-metal` is an
artifact-storage category, not native acceptance. Captured text is normalized
for repository whitespace; `RAW_SHA256SUMS` records the original capture digests.

Same-boot physical reclamation, pending transfers, guest serving recovery,
incomplete spare creation, supported native host-reboot acceptance and filesystem
power-loss qualification remain pending. This path deletes no physical resources
and establishes no customer drain receipt.
