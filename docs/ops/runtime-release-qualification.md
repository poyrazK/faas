# Collecting and importing native runtime qualification evidence

This private operator workflow implements
[ADR-687](../adr/687-guarded-native-runtime-qualification-collector.md) and
[ADR-686](../adr/686-trusted-runtime-qualification-import.md)
and records the [ADR-685](../adr/685-native-runtime-release-qualification.md)
receipt consumed by explicit upgrade preparation. Publishing a runtime,
scanning it, passing generic metal smoke tests or hashing synthetic logs does
not qualify it. Customer upgrade previews remain read-only.

## Trust and native prerequisites

Select the expected release ID, dedicated native host UUID, source/harness
commit and a fresh run UUID before accepting evidence. Pin the trusted native
owner's Ed25519 public key from operator configuration. Never take that key or
these pins from the submitted bundle. Protect the private key in the native
acceptance owner; the importer needs only the public key. There is no automatic
key discovery, key enrollment or customer qualification API.

The native owner must verify the designated Linux amd64 host without nested
virtualization, accessible `/dev/kvm`, root privileges, cgroups v2 and
`/etc/faas/builder-acceptance-host`. Drain that acceptance host and hold its
exclusive acceptance lock for staging, testing and final leakcheck. Production
nodes are not acceptance substitutes. The collector holds the shared builder/
metal/e2e lock through native ownership and restores originally active services
before signing. It verifies cpu/memory/pids controllers, disabled unprivileged
user namespaces, tenant bridge and IPv4 forwarding. Do not queue other native
gates while this host requires recovery.

Provision a trusted root-owned Git clone with its own `.git`, protected resolved
ancestors and no group/other-writable files. Linked worktrees, Git alternates,
symlink entries in `.git` and uncommitted `.git/info/attributes` are unsupported.
The collector archives only the selected committed object, disables replacement
objects/hooks/fsmonitor/global attributes, rejects unsafe archive entries and
writes its own source marker. It never builds dirty/untracked source. Provision
the protected `/srv/fc/acceptance` staging parent in advance. Provision
the exact source `go.mod` version in a protected Go distribution and pin the Go
executable SHA-256 independently. The distribution and host utilities are trusted
operator installations. Required fixed-PATH tools are git, bash, firecracker,
jailer, systemctl, systemd-detect-virt, ip, iptables, nft, tc, gcc, debugfs, python3
and readlink. Verify the installed Firecracker version and independently pin
its bytes and the protected host kernel in the fixture.

Select an existing managed function deployment whose immutable layer has the
physical account/runtime binding to the exact catalogue release. Its handler
must return HTTP 200 at `/` with the exact body
`gregale-runtime-qualified:<runtime>` (for example,
`gregale-runtime-qualified:node22`). Use its actual published layer and the
release's actual immutable base; no busybox replacement or synthetic rootfs.
Stage kernel/base/layer as distinct verified local files. The native test hashes
these files, the resolved Firecracker executable and `/sbin/init` inside base
ext4 via `debugfs`. It checks artifact hashes again after retirement.

## Capturing the exact attempt

Write a bounded JSON `runtimequalification.Fixture` containing the complete
`state.RuntimeRelease` target plus `run_id`, `host_id`, `source_commit`,
`deployment_id`, `layer_key`, `layer_sha256`, `kernel_sha256` and
`firecracker_sha256`. The nested target uses the existing Go RuntimeRelease JSON
field names (`ID`, `Runtime`, `Architecture`, `SourceRef`, `GuestInitSHA256`,
`LayoutVersion`, `BaseSHA256`, `CreatedAt`). Marshal these types directly;
release identity must validate over all immutable components. The host UUID is
canonical `/etc/machine-id`; the run UUID is unique to this selected attempt.

Use existing operator `DATABASE_URL` and artifact backend configuration in the
collector environment. The database must already be migrated. Build the private
tool from reviewed source with `go build ./cmd/runtime-qualification-collect`.
Run it as root only on the selected drained acceptance host. Supply a protected
root-owned 0600 regular file with one link containing exactly 32 binary seed
bytes. Keep it outside resolved source and output paths; a final symlink is
rejected. Its derived public key must match the independent lowercase hex pin.
Provisioning that key is an operator trust decision, not bundle enrollment.

The output directory must not exist and must have a protected root-owned parent
outside source. Select a fresh run UUID and output path for every attempt:

```sh
./runtime-qualification-collect \
  -fixture "$QUAL_FIXTURE_PATH" \
  -signing-seed-file "$QUAL_PROTECTED_SEED_PATH" \
  -public-key "$QUAL_TRUSTED_PUBLIC_KEY_HEX" \
  -release "$QUAL_EXPECTED_RELEASE_ID" \
  -host "$QUAL_EXPECTED_HOST_UUID" \
  -source-commit "$QUAL_EXPECTED_SOURCE_COMMIT" \
  -run "$QUAL_EXPECTED_RUN_UUID" \
  -source-dir "$QUAL_PROTECTED_SOURCE_DIRECTORY" \
  -go "$QUAL_PINNED_GO_EXECUTABLE" \
  -go-sha256 "$QUAL_PINNED_GO_SHA256" \
  -kernel "$QUAL_PROTECTED_KERNEL_PATH" \
  -firecracker-version "$QUAL_PINNED_FIRECRACKER_VERSION" \
  -output-dir "$QUAL_NEW_EVIDENCE_DIRECTORY"
```

The collector verifies published metadata/binding, holds the exclusive lock,
archives source, stages exact published assets and builds static vmmd/jail
helpers plus a race-enabled metal binary. It checks drain before and after
stopping originally active services, with schedd first and vmmd last. Child
commands receive private caches/tmp/home and a fixed environment without operator
credentials, signing paths, ambient GOFLAGS or proxy hooks. Dependency downloads
use the pinned source's Go module configuration and the Go defaults.

It executes exactly `TestMetalRuntimeReleaseColdBootReady` through real Go
test2json, then final leakcheck after that process has stopped. Raw
`test-metal.jsonl`, stderr, `leakcheck.log`, stderr, build logs and
`process-exits.json` remain in the output directory. Any nonzero exit, skip,
borrowed/incomplete observation or failed cleanup is refused. Generic
`make test-metal` output does not establish this exact-runtime profile.

After successful coverage verification, host/asset rechecks, reverse service
restoration and staging removal, the collector signs version 1
`runtime-upgrade-native-v1` evidence, syncs `report.json` and its directory,
then automatically invokes the existing importer. This records eligibility
only after published-byte verification and retained audit readback. No manually
assembled report or synthetic local trace should be used as native evidence.

## Failure and recovery

Every attempt uses private staging at
`/srv/fc/acceptance/runtime-qualification-<run-uuid>`. The `active-services` file
records original active units durably before stops. Failed test/cancellation
still attempts final leakcheck with a fresh cleanup context and service
restoration. Signing never follows failed test, leakcheck, recheck or restoration.

If resources remain, the collector leaves services stopped and staging retained;
it does not reap unknown processes, namespaces, mounts or loops. If restoration
fails, staging and the recovery list remain as well. Another collector attempt
refuses any retained qualification staging. SIGKILL, power loss or reboot cannot
run cleanup, so inspect the same retained directory after interruption. Hold
the shared acceptance lock during operator recovery, investigate and retire
the owned resources, require a successful native leakcheck, then restore the
recorded units in reverse order and verify service health. Remove only that
recovered run's staging after these checks. Other acceptance gates do not inspect
this recovery record; keep them stopped until recovery is complete.

The runner's command-group cancellation helps stop owned children; Firecracker
can use a separate session, so only final resource checks establish retirement.
No automatic repair of leaked resources or customer metadata is part of this
workflow. Diagnostic evidence output remains even after clean failed attempts.

## Import and retention

If automatic import fails after successful collection, retry the retained bundle
with the private importer and the same independent pins. Use existing operator
`DATABASE_URL` and artifact backend configuration in the import environment.
The command connects to the already migrated database and
published artifact backend; it performs no migrations or native execution.
Build the private tool with `go build ./cmd/runtime-qualification-import`.
Then pass the independently selected pins:

```sh
./runtime-qualification-import \
  -report "$QUAL_REPORT_PATH" \
  -test-metal "$QUAL_METAL_PATH" \
  -leakcheck "$QUAL_LEAKCHECK_PATH" \
  -public-key "$QUAL_TRUSTED_PUBLIC_KEY_HEX" \
  -release "$QUAL_EXPECTED_RELEASE_ID" \
  -host "$QUAL_EXPECTED_HOST_UUID" \
  -source-commit "$QUAL_EXPECTED_SOURCE_COMMIT" \
  -run "$QUAL_EXPECTED_RUN_UUID"
```

The public key is 32 bytes in lowercase hex. No private key is accepted by this
command. It validates signature and log coverage before opening infrastructure,
then verifies published base/layer bytes and managed function binding. It
retains `report.json`, `test-metal.jsonl` and `leakcheck.log` under
`qualification/runtime-releases/<release>/<canonical-envelope-sha256>/`, checks
readback, and finally asks the ledger to record qualification. The success line
includes release, report digest and original server recording time. The report
digest covers the canonical signed envelope, including signer identifier and
signature; raw log digests are preserved separately.

Limits are 64 KiB per envelope/report/fixture JSON, 64 MiB per log, 256 KiB per
Go JSON event or leakcheck line, and 16 JSON nesting levels. Published artifacts
are streamed when hashed. Identical imports retain the receipt timestamp and
reuse audit objects. Different evidence cannot replace a receipt. Revoked
releases cannot be revived by import; ADR-685 has no reset/supersession switch.
Corrupt retained evidence is rejected without overwriting it. Failed imports
can leave unreferenced audit objects but never a qualification receipt.

Collection bounds are 2 GiB per staged asset and 512 MiB for the whole source
archive including headers/padding. Lock wait is 15 minutes with 100 ms polling;
source/tool/drain and build preparation budgets are 10 minutes each, the test
budget is 3 minutes, host probes/cleanup use 2-minute budgets, and command pipe
wait delay is 5 seconds. Build
time is outside the native test interval. Policies live in `pkg/api/limits.go`.

This receipt gates build preparation only. A customer upgrade still needs its
own fresh candidate boot/readiness, retained-baseline checks, guarded rollout
and rollback, and an authoritative cutover transaction that checks revocation.
The apply endpoint, maintenance scheduler and actual designated-host acceptance
are subsequent work. No real release has been qualified by local development tests.
