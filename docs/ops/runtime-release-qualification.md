# Importing native runtime qualification evidence

This private operator workflow implements [ADR-600](../adr/600-trusted-runtime-qualification-import.md)
and records the [ADR-599](../adr/599-native-runtime-release-qualification.md)
receipt consumed by explicit upgrade preparation. Publishing a runtime,
scanning it, passing generic metal smoke tests or hashing synthetic logs does
not qualify it. Customer upgrade previews remain read-only.

## Trust and native prerequisites

Select the expected release ID, dedicated native host UUID, source/harness
commit and a fresh run UUID before accepting evidence. Pin the trusted native
owner's Ed25519 public key from operator configuration. Never take that key or
these pins from the submitted bundle. Protect the private key in the native
acceptance owner; the importer needs only the public key. There is no automatic
key discovery, key enrollment, signer command or customer qualification API.

The native owner must verify the designated Linux amd64 host without nested
virtualization, accessible `/dev/kvm`, root privileges, cgroups v2 and
`/etc/faas/builder-acceptance-host`. Drain that acceptance host and hold its
exclusive acceptance lock for staging, testing and final leakcheck. Production
nodes are not acceptance substitutes. The new test neither drains services nor
acquires this host lock; an automated guarded collector is still pending.

Use a verified checkout at the pinned commit, with its static vmmd test helper
built by the existing metal build workflow. For an archive, the trusted transfer
owner must verify source provenance before writing
`.faas-runtime-qualification-source-sha` at the archive root. The test otherwise
reads Git HEAD. A marker supplied by an untrusted archive is not provenance.
The signed owner is responsible for the checkout contents and tool integrity.

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

Set `FAAS_RUNTIME_QUALIFICATION_FIXTURE`, `FAAS_TEST_KERNEL`,
`FAAS_TEST_BASE_ROOTFS`, `FAAS_TEST_LAYER_ROOTFS`, `FAAS_TEST_VMMD_BINARY` and
the pinned `FAAS_TEST_FC_VERSION`. Under the guarded native owner's lock, run the
named metal test with Go JSON output and then the repository final leakcheck:

```sh
go test -tags metal -json -count=1 -timeout=3m \
  -run '^TestMetalRuntimeReleaseColdBootReady$' ./pkg/fcvm > test-metal.jsonl 2> test-metal.stderr
make leakcheck > leakcheck.log 2>&1
```

Capture each process exit independently, and reject any nonzero exit. Preserve
raw JSON/log bytes. Do not pipe through tools that lose the producer's exit
status. Final leakcheck must run after test cleanup; the test's in-process
check is additional evidence. Missing fixture selection, skipped tests and a
non-Linux leakcheck no-op are refused. A qualifying JSON stream contains only
this named test and its package verdict. Generic `make test-metal` output can
contain unrelated tests/skips and is not this profile's import log.

Build a `runtimequalification.Report` with version 1,
`runtime-upgrade-native-v1`, UTC start/completion interval, the exact emitted
`GREGALE_RUNTIME_QUALIFICATION=` observation, SHA-256 of both raw logs, and both
explicit process exit codes. The protected native owner must verify the actual
host, successful command exits, matching observed identities and final ordering
before calling `runtimequalification.EncodeEnvelope(report, metal, leak, key)`.
That helper verifies shape and log coverage before signing; it cannot establish
physical host trust on its own. The report/envelope cannot carry its own trust
anchor. Do not manufacture this report from local macOS test fixtures.

## Import and retention

Use existing operator `DATABASE_URL` and artifact backend configuration in the
import environment. The command connects to the already migrated database and
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
releases cannot be revived by import; ADR-599 has no reset/supersession switch.
Corrupt retained evidence is rejected without overwriting it. Failed imports
can leave unreferenced audit objects but never a qualification receipt.

This receipt gates build preparation only. A customer upgrade still needs its
own fresh candidate boot/readiness, retained-baseline checks, guarded rollout
and rollback, and an authoritative cutover transaction that checks revocation.
The apply endpoint, maintenance scheduler and automated native collector are
subsequent work. No real release has been qualified by local development tests.
