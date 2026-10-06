# Prepared-network boot intent diagnostics — 2026-10-03

These are internal GCP **nested virtualization diagnostics**, not supported native
x86_64 KVM acceptance. Customer cutover stays disabled. The source implements
[ADR-405](../../../adr/405-managed-postgres-prepared-boot-intent.md).

## Source and host

- Final Linux validation snapshot: `be4bcd30dbf7cb807a014b954e728dea87dd9534`; parent `bfb4925db5e1f38db10b8b7afd43da2b920bdf57`.
- Source tree: `4a625219df4479754e4446bb330bba1621cde14e`.
- Archive SHA-256: `adacc55a65a0dc4466c921b51918a744368b3391c2ea5ae43f19b397fc17a336`.
- Change patch SHA-256: `4d77e80df903706795f619da7c2cde850582c51ab3a36cc707a076118e0c0d61`.
- Host: `gregale-prod / us-east1-b / gregale-internal-test-1`, nested-enabled
  `n2-highmem-2`, Intel Cascade Lake, Go 1.25.13, Firecracker fixture version 1.7.0.
- Final unit: `gregale-managed-pg-prepared-boot-d4841725-final`; invocation `fc8f7d5b28b24fb3bf1ce8ab764b1303`.

[validation.json](validation.json) pins the source and runner inputs. The source
archive is verified before extraction, and [source-runtime-match.log](source-runtime-match.log)
checks SHA-256 of 4234 selected Go/module files after Linux
execution. Whole-workspace checks compare Git blob hashes of the actual bytes of
all 6,265 Go/module files against the final snapshot.

The full macOS race suites passed on snapshot `d45c57a71db9b00253a2a0509d0c689f72adcb75`.
[darwin-source-match.log](darwin-source-match.log) confirms the only subsequent
source changes are two tests guarded by Linux build tags; all Darwin implementation,
test and module inputs are byte-identical to the final snapshot. The successful
macOS run uses the standalone pinned SDK, `-vet=off` and `-ldflags='-s -w'`.
Default Linux vet and changed-code lint provide static checks. Nonfatal Darwin
linker warnings are preserved in [portable-macos.log](portable-macos.log).

## Results

The bounded `make test-metal` selection passed **83 top-level
tests (347 including subtests)** with zero failures or skips.
[metal.log](metal.log) includes 11 separate-process SIGKILL checkpoints: initial
intent, namespace intent, created namespace before checkpoint, namespace
checkpoint, link intent, created link before checkpoint, completed spare,
transfer intent, dual aliases, moved alias and guest intent. Every same-boot
record retains its quarantine, including records with no physical names.
Observed namespaces and host links survive recovery unchanged. The guest-intent
crash fixture launches no VMM; other selected tests exercise real guest/process
and image-bind provenance.

All 35 prior-boot cases passed across legacy complete records and version-6
initial/partial/complete/retired records. For each record shape, absent resources
permit durable retirement, while a real foreign namespace, veth, dummy link or
unrelated process under the reserved UID retains the reservation and survives
unchanged. Prior-boot provenance is injected; these tests perform no real host
reboot and supply no native reboot or power-loss acceptance.

Full fcvm/vmmd race suites passed on Linux and macOS. Linux vmmd portable tests
run as `nobody` from the package working directory. Portable regressions cover
boot commit before physical setup, unknown/changed boot probes, contradictory
records, boot preservation through adoption, version-5 compatibility, partial
record retirement, same-boot/guest-transfer retention and failed-fsync retry.
Final test and lint exit files are zero. Changed-code lint, egress render parity,
kernel nft syntax, bridge-name and generated deployment checks passed. Static
policy and test-citation checks passed. Image validation skipped because Packer
is absent; this change edits no image templates.

Three final lifecycle leak checks passed before tests, after metal and on exit,
all under the shared acceptance lock. [unit-journal.log](unit-journal.log) confirms
successful completion; transient-unit default status after unload is not used
as proof. [download-verification.log](download-verification.log) verifies the
complete result archive SHA-256 before removing the stage.

## Preserved failures and cleanup

The initial Linux fcvm.test process hit the 10 GiB unit memory cgroup limit.
[oom-diagnostic.log](oom-diagnostic.log) preserves kernel attribution and
`initial-oom-*` preserves its runner, output and failed-unit status. It produced
no successful portable or metal gate. The retry reduces GOMEMLIMIT to 384 MiB
and GOGC to 10, retains the same source and enables verbose portable output.

That retry exposed the existing Linux fault-around fixture's assumption that an
mmap base is aligned to 64 KiB. Its actual mapping address can be only page-aligned
under ASLR, so a legitimate kernel range fell outside the expected file-offset
window. `unaligned-prefetch-*` preserves that failed gate. The test now computes
the expected window from the actual virtual address and clips it to file bounds.
The final run passes with these Linux-only fixture corrections; production code is unchanged
between the original macOS snapshot and final Linux snapshot.

The following metal run passed all 11 SIGKILL stages but failed one foreign-link
identity assertion among the 35 prior-boot cases. `foreign-link-failed-*`
preserves this failed gate. Recovery issued no physical commands. The node has
active udev with persistent-MAC policy ([link-policy.log](link-policy.log));
asynchronous replacement of a kernel-random veth MAC is a plausible cause.
The fixture now creates its own explicit address and reports both identities on
failure, while retaining strict equality. The final metal run passes all cases.
This adjusts only the test fixture, never the node's link policy or other tasks.
Lint finished successfully before an attempted stop of the already-failed run's
linter; its cgroup was already gone, so no process was signalled.
[stop-failed-lint.log](stop-failed-lint.log) preserves that harmless failed stop
attempt. The final runner keeps tests at 384 MiB/GOGC=10 and uses 2 GiB/GOGC=50
for lint to reduce GC pressure. Other jobs and system services were untouched.

The first macOS command stopped during package loading because its downloaded
Go toolchain lacked a standard-library vendor package. The standalone pinned
SDK retry then exhausted local disk space during vet-file creation. Both failed
attempts are preserved; neither is counted as a successful gate. An initial
private-index staging attempt also hit local disk exhaustion before producing
an archive or branch mutation. [staging-notes.log](staging-notes.log) records these
limitations and the successful retry configuration.

[cleanup.log](cleanup.log) confirms all four owned units are inactive/reset and
the registered stage was removed after collecting verified results. Private
Darwin cache and temporary paths were removed; shared caches and other tasks
were untouched. Registry kind `native-metal` is an artifact-storage category,
not native acceptance. Captured text is normalized for repository whitespace;
`RAW_SHA256SUMS` records original capture digests, including empty logs.

Same-boot physical reclamation, pending transfers, guest serving recovery,
supported native host-reboot acceptance and filesystem power-loss qualification
remain pending. Customer activation stays disabled. Recovery deletes no physical
resources and establishes no customer drain receipt. Version 6 requires a
compatible binary or stopped/drained journal for rollback.
