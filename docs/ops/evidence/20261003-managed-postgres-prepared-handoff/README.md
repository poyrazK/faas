# Prepared-network handoff diagnostics — 2026-10-03

These are internal GCP **nested virtualization diagnostics**, not supported native
x86_64 KVM acceptance. Customer cutover stays disabled. The source implements
[ADR-403](../../../adr/403-managed-postgres-prepared-network-journal.md).

## Source and host

- Validation snapshot: `be2e765510687167a9d01e550276417043fba919`; parent `596de3bbc1e146e234fb2fc987b38183b8fef9fa`.
- Source tree: `961d9c5daa8a4e797f20e8615dd32c274477791a`.
- Functional archive SHA-256: `3f1265867816f693da455a02cc5eb23bebafac6baca20ec8a35866b839918338`.
- Change patch SHA-256: `60706c586e500852d075c733eeb44174b1cf919ed730af021e010ce79bd7ce93`.
- Node: `gregale-prod / us-east1-b / gregale-internal-test-1`, nested-enabled
  `n2-highmem-2`, Intel Cascade Lake, Go 1.25.13, Firecracker 1.7.0.
- Functional unit: `gregale-managed-pg-prepared-handoff-6ac4ffcb-complete`, invocation `b0240f38dfa8470782ac88b1b5e506c4`.
- Supplemental checks unit: `gregale-managed-pg-prepared-handoff-6ac4ffcb-checks`, invocation
  `785f2d901b624bcfba8697a640e56116`.

[validation.json](validation.json) pins the immutable source, archives, patch,
runner inputs and supplemental renderer. [source-hashes.sha256](source-hashes.sha256)
covers 4229 selected Go/module files; the unchanged renderer adds
one verified file. [final-source-match.log](final-source-match.log) confirms all
6,261 workspace Go/module files still match the snapshot. Runtime verification
in [source-runtime-match.log](source-runtime-match.log) and
[renderer-runtime.sha256](renderer-runtime.sha256) checks 4230
files after execution.

## Results

The bounded `make test-metal` selection passed **71 top-level
tests (212 including subtests)** with no skips or failures.
[metal.log](metal.log) includes five real process SIGKILL checkpoints: unused
spare, transfer intent, dual namespace aliases, moved alias and committed guest
intent. Each reopened journal preserves the nsfs/veth resources, fences identities
and the slot, and refuses destructive recovery. The committed-guest crash fixture
records guest intent without launching a VMM; existing surviving-guest tests cover
real Firecracker process/bind provenance separately.

Full fcvm/vmmd race suites passed on both Linux and macOS. Linux runs vmmd's
permission-sensitive portable suite as `nobody` in its package working directory.
[portable-linux.exit](portable-linux.exit), [metal.exit](metal.exit),
[linux-lint.exit](linux-lint.exit) and [portable-macos.exit](portable-macos.exit)
are zero. Changed-code lint reports zero issues. The selection also covers
prepared policy reuse, placement/link fences, asset intent, restart quarantine,
retained teardown, failure reporting and daemon configuration.

Three lifecycle leak checks in [run.log](run.log) passed before, after and on
exit from the functional run. Two additional leak checks surround the
supplemental checks; all ran while holding the shared acceptance lock. Final
egress render parity, nft kernel syntax, bridge-name and generated deployment
checks passed. Static encoding, quoting, dependency, sealed-environment, runbook
SQL, ADR-number and test-citation checks passed. Packer validation skipped because
Packer is absent; the change does not edit image templates.

The functional unit itself exited 2 at the subsequent egress packaging check.
Its archive omitted `cmd/faas-nft-render`; [egress-check.log](egress-check.log)
preserves this failure. The unchanged renderer was supplied from the same pinned
snapshot, and the remaining checks then passed in a separate unit:
[egress-check-final.log](egress-check-final.log), [checks.exit](checks.exit) and
[checks.log](checks.log). No implementation code changed between these runs.
The failure is not counted as a successful complete-unit gate.

Two earlier runner failures are preserved. `initial-failed-*` records the
read-only-directory test run as root, which bypasses its intended permission
failure. `unprivileged-failed-*` records two relative-path fixture failures when
the compiled test binary ran outside its package directory. Both were repaired
in the runner, with full suites subsequently passing. [policy.log](policy.log)
also preserves a local disk-space failure; final egress verification ran on Linux
and static policies passed separately.

## Limits and cleanup

Recovered records grant no live ownership or automatic reclamation. Physical
alias power-loss qualification, complete resource incarnations, peer/TUN and
loop/parent mounts, jail staging, snapshot publication, serving recovery and
all-node drain proof remain pending. ADR-402's privileged-mutation and interface
index-reuse limitations remain. Supported native lifecycle acceptance is pending.

[cleanup.log](cleanup.log) confirms that all task units became inactive and only
the owned temporary disk stage was removed after the downloaded archive hash
matched. The task-private macOS cache was removed after its race suites passed.

## Artifact integrity

[SHA256SUMS](SHA256SUMS) covers committed evidence bytes. `RAW_SHA256SUMS` records
captured bytes before trailing-whitespace normalization. Joining the `lines` in
[source-change.patch.json](source-change.patch.json) reconstructs the exact pinned
patch. Raw source and result archives remain in private local scratch storage.
