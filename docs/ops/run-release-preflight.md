# Run release preflight and rollback

Use `vmmd run-release` from the verified candidate release. These local,
read-only commands prepare and check a release; the existing signed installer,
node rollout barriers, runtime admission and qualification still control
activation. Run the checks on every target node with the same environment
that its vmmd unit will use. Hold the existing rollout/drain barrier while
checking and replacing a binary. A saved passing result is not a reusable
enablement token or proof that the fleet installed its readers.

The [2026-11-14 verification](evidence/20261114-run-release-preflight/README.md)
records the Linux build, race regressions, lint and direct CLI checks on the
internal node, including the expected production-preflight hold.

## Build the reviewed plan

Obtain candidate and rollback binaries through the normal signed release
installation process. Use reader manifests packaged by releases known to
implement this subcommand. For a newly built compatible release:

```sh
umask 077
/opt/faas/candidate/vmmd run-release capabilities > /var/lib/faas/run-release/candidate-reader.json
/opt/faas/compatible-rollback/vmmd run-release capabilities > /var/lib/faas/run-release/rollback-reader.json
```

Only use `capabilities` on binaries already known to implement it. Do not
probe arbitrary older vmmd binaries: an unknown verb can start their daemon.
A missing or incompatible reader manifest blocks the release. All readers,
including the rollback target, must support launch-owner 3, template-flight 1,
nested runtime authority 2, KVM-only device scope 2 and publication intent 2.
The check binds each declaration to its binary SHA-256. Release authenticity
comes from the signed release bundle and independent operator review.

Prepare a candidate TOML with `native_process_recovery`,
`native_launch_owner_records` and `native_runtime_capture` enabled. Prepare a
separate rollback TOML with `native_runtime_capture = false`, native recovery
and owners still enabled, and all native/resource paths and volatile-write
settings retained. Keep adoption, qualification capture and prepared-network
caching off in this profile. Parse/validation uses vmmd's normal loader and
the supplied deployment environment; keep the candidate and rollback unit
environments consistent with the environment used for this check.

Operator-prepared persistent roots must already exist with their normal
private permissions. The native launch journal may be absent before first
enablement; the tool will not create it or infer older-reader compatibility.

Generate a draft using the actual release paths and qualified source commit:

```sh
/opt/faas/candidate/vmmd run-release prepare \
  --source-commit QUALIFIED_SOURCE_COMMIT \
  --candidate-reader /var/lib/faas/run-release/candidate-reader.json \
  --rollback-binary /opt/faas/compatible-rollback/vmmd \
  --rollback-reader /var/lib/faas/run-release/rollback-reader.json \
  --config /var/lib/faas/run-release/candidate.toml \
  --rollback-config /var/lib/faas/run-release/rollback.toml \
  --artifact-kernel /srv/fc/base/vmlinux \
  --artifact-base /srv/fc/base/runner-node24-amd64.ext4 \
  --artifact-guest_executor /opt/faas/candidate/guest-init \
  --artifact-firecracker /opt/firecracker/firecracker \
  --artifact-jailer /opt/firecracker/jailer \
  --artifact-jail_helper /opt/faas/candidate/vmmd-jail-helper \
  --acceptance /var/lib/faas/run-release/native-summary.json \
  > /var/lib/faas/run-release/plan.json
```

`prepare` hashes files and emits JSON. It does not approve artifacts, execute
the referenced rollback binary or attest qualification. Review the paths,
source identity, hashes and release signatures independently before using
the plan. Rebuild the plan after any input changes; do not overwrite a saved
plan/result that records an attempted release. No automatic chmod, directory
creation, state deletion or configuration repair occurs.

Use resolved, versioned files for binaries and runtime artifacts. File symlinks
are refused, including convenience links such as `/usr/local/bin/firecracker`.

## Production preflight

```sh
/opt/faas/candidate/vmmd run-release preflight \
  --plan /var/lib/faas/run-release/plan.json \
  > /var/lib/faas/run-release/preflight.json
```

Exit 0 means this local check passed. Exit 3 means hold the rollout and inspect
the JSON checks. Exit 1 means invalid input or a config/read failure; parser
diagnostics and TOML contents are not exported. An invocation error must also
hold the rollout. The command has a two-minute deadline; input JSON is bounded
to 2 MiB and inventories to 100,000 entries per root. Runtime binaries, rootfs
and kernel artifacts are streamed without the JSON size cap.

The host probe requires Linux amd64, accessible KVM API 12, cgroups v2 and an
explicit `systemd-detect-virt` result of `none`. Missing/unknown virtualization
does not pass. Inventories count directories and regular files; symlinks and
special files block. Counts do not establish valid ownership or zero leaks.

The independently reviewed native acceptance summary uses
`gregale.run-native-recovery-summary/v1`. Production preflight requires:

- `functional_pass`, `bare_metal_host`, `p0_3_complete` and
  `runtime_release_eligibility` all true, with the native owner-v3 profile.
- The exact qualified source and a nonvirtualized x86_64 KVM observation.
- `release_artifact_sha256` containing measured `vmmd`, `kernel`, `base`,
  `guest_executor`, `firecracker`, `jailer` and `jail_helper` hashes matching
  the plan. Obtain these from the native acceptance owner; adding hashes to
  an old report does not qualify a changed binary or runtime image.
- Every named scenario in the ten native recovery, nine template/clone and
  seven warm/OOM matrices, successful process/leak exits, 27 customer Runs
  reconciled exactly once, erased payloads, zero template-account Runs,
  27 KVM-only wake receipts and all within-fixture cleanup checks.

The existing internal-node report is functional-only, reports Google
virtualization and lacks production release eligibility. It intentionally
cannot pass. The current acceptance exporter never sets that eligibility;
supported-native qualification and evidence export remain the next gate.
Do not turn report fields true manually. Existing image admission and ADR-739
qualification provide independent provenance/eligibility checks at runtime.

## Compatible rollback

```sh
/opt/faas/candidate/vmmd run-release rollback \
  --plan /var/lib/faas/run-release/plan.json \
  > /var/lib/faas/run-release/rollback-check.json
```

This checks the pinned compatible target and writer-disabled configuration;
it does not install them. It retains the acceptance bytes but does not require
the failed candidate to have passed new-release qualification. Consequently a
compatible rollback can be checked on the internal KVM node while production
preflight remains blocked. Its JSON has `production_enablement_checked=false`
and `runtime_admission_granted=false`.

1. Disable schedd's `FAAS_EXECUTION_TEMPLATE_CAPTURE` through the existing
   rollout mechanism. Retain the platform account, catalog and artifact roots.
2. Use the existing node drain and operation-completion barrier. Let the
   current compatible vmmd retire work and capture flights. Inventory counts
   do not replace this barrier or authorize killing a process.
3. Re-run `rollback` under the rollout lock immediately before replacement.
   Require exit 0, then install only the pinned compatible binary/configuration
   using the normal signed installer. Keep native recovery and launch-owner
   readers enabled; keep all retained paths and audit records.
4. Run base-adoption, restart/reconnect, physical cleanup and usage checks
   before undraining. Retain cold fallback for incompatible templates.

The tool supplies no older-reader downgrade, migration, automatic rollback,
root removal or catalog GC. An empty native journal does not relax the reader
gate. Retained incomplete captures must not be deleted to make an old release
start. An incompatible target requires a compatible rollback release or a
separately designed state migration.

See the [decision](../adr/20261114020000-run-release-preflight-rollback.md),
[lifecycle acceptance](run-lifecycle-acceptance.md) and
[Run roadmap](../gregale-run-roadmap.md).
