# Gregale CI native acceptance

`make native-ci-acceptance` runs the opt-in CI harness on a dedicated Linux
x86_64 KVM host. It starts its own candidate daemons and owns a random test
schema, local artifact root and private evidence directory. It never changes
deployed CI flags. The normal metal matrix does not automatically run this suite.

## Prerequisites

Use a drained native acceptance host with root access, `/dev/kvm`, Firecracker,
jailer, cgroups v2, the tenant bridge/NAT setup, a staged kernel, Go 1.26.10 and
the existing native host tools. Allow at least five 512 MiB CI VMs plus the
128 MiB tenant probe and platform daemons; a four-vCPU, 16 GiB host is a suitable
starting point. The gate shares `/var/lock/faas-builder-acceptance.lock` with the
other native suites and refuses active Gregale services or existing workloads.

Provision `/etc/faas/ci-acceptance-host` as a root-owned regular file without
group/other write permissions. This host designation is required before any
test setup. Do not point the gate at a control-plane database or production
bucket. The dedicated test database must have `citext` installed in `public`,
permit schema creation/migrations, and have no migration ledger in `public`.

Build the candidate runner image through the existing isolated image build
process using `images/ci-runner.Dockerfile`, then supply its linux/amd64 manifest
digest. The harness does not publish or build that image. It compiles the static
candidate runner using the recipe's flags and checks the same binary SHA-256
inside each guest. Include `sh`, `sha256sum`, `dd`, `sleep`, CA trust and the
runner binary, as the recipe does. The native checkout must be clean.

Configure an existing dedicated GCS bucket with public access prevention
enforced, uniform bucket-level access, no versioning, no soft delete, no
retention policy and no lifecycle rules. Provide the service identity through
normal application default credentials. The identity needs bucket attributes,
object read/create and conditional replacement permissions. No bucket listing,
IAM mutation or public URL is used. Emulators cannot qualify this gate.

The HTTPS origin must resolve to the dedicated host, be reachable through normal
tenant egress, and use a certificate trusted by the runner image and host. Bind
the matching free TLS port; no Caddy configuration is changed. Do not use a
loopback/private/link-local origin or disable TLS verification. The TLS key must
be a root-owned private regular file. Its contents and provider credentials are
never written into evidence or passed to guests.

Export these settings from the operator's protected environment:

| Setting | Meaning |
|---|---|
| `GREGALE_CI_NATIVE_ACCEPTANCE=1` | Explicit test opt-in; no production CI flag. |
| `GREGALE_CI_ACCEPTANCE_DATABASE_URL` | Dedicated test database DSN, never a command-line argument. |
| `GREGALE_CI_ACCEPTANCE_GCS_BUCKET` | Existing qualified dedicated bucket. |
| `GREGALE_CI_ACCEPTANCE_IMAGE` | Candidate runner's immutable linux/amd64 OCI reference. |
| `GREGALE_CI_ACCEPTANCE_ORIGIN` | Guest-reachable HTTPS origin, e.g. `https://ci-native.example.net:9443`. |
| `GREGALE_CI_ACCEPTANCE_LISTEN` | Matching free local listener, e.g. `0.0.0.0:9443`. |
| `GREGALE_CI_ACCEPTANCE_TLS_CERT` / `GREGALE_CI_ACCEPTANCE_TLS_KEY` | Certificate chain and private key paths. |
| `FAAS_TEST_KERNEL` | Staged native guest kernel path. |
| `FAAS_PUBLIC_IFACE` | Tenant egress NIC; otherwise resolved from the default route. |
| `GOOGLE_APPLICATION_CREDENTIALS` | Optional existing ADC credential selector; never print or copy its contents. |

The runner derives the source SHA and binary hashes. Leave production
`FAAS_CI_*` settings at their disabled defaults. Only the fixture children receive
enabled CI policy, the apid worker and a one-account allowlist.
Revision/binding/execution admission, status, logs, cancellation and downloads
use authenticated HTTPS. The subprocess worker alone advances normal execution;
a scoped synthetic githubd gRPC service supplies the fixed source archive. Local images, scans and daemon
binaries use this run's artifact root, so no fleet artifact-store setting changes.

## Run and assess

```sh
make native-ci-acceptance
```

Evidence appears in a fresh `/var/tmp/gregale-ci-native/run-XXXXXXXX` directory,
mode 0700. An optional evidence root must remain under the
`/var/tmp/gregale-ci-native-<suffix>` namespace. The gate runs six scenarios:
two dependent jobs, a user failure, timeout, cancellation, daemon/controller
restart with lost upload ACK, and a tenant wake during five CI CPU workloads.
The 300 KiB output exercises real GCS resumable uploads and verified downloads.
The metering assertion compares the real residency journal with emitted job
MB-seconds, including the existing 8 MiB overhead.

The source archive and GitHub binding are fixed synthetic fixtures. Native
qualification does not establish real GitHub App access. The contention check
does not certify full-host RAM exhaustion, tenant priority under every pressure
condition, or the reference-SSD wake latency SLO.

`journal.json` contains source/binary/image identities, execution and native
instance IDs, scenario checks and retired physical object descriptors. It omits
DSNs, bearer keys, capabilities, private keys and output bodies. Keep the entire
directory private: native daemon files and fixture keys may remain after failure.
Archive only the reviewed JSON/exit/log evidence, never the whole directory.

A passing exit requires every scenario receipt, successful Go tests, provider
retirement confirmation, the native leak gates and removal of the owned schema
and native files. Missing/skipped tests, stale runner images, ambiguous stop,
provider errors or cleanup failures remain failures. Empty GCS tombstones remain
permanent; do not delete them or add lifecycle rules. Reconcile accumulated
dedicated-bucket tombstones through a separate reviewed operator procedure.

## Interrupted runs

EXIT/TERM/INT/HUP cleanup has its own budget. It cancels CI intent, waits for the
real owners to stop VMs, retires journaled data, terminates the owned process
group and asserts leaks. It does not reap host resources to make the check pass.
SIGKILL or a host crash can leave the schema, native journals and payload quota
retained. Never delete those records or start a new drill over active VMs.

On the same dedicated host, retain the original database/bucket settings and
inspect the failed run's journal. Reconcile any native owner/resource journal
first, keeping its native files available. The stored schema name and immutable
marker limit recovery to that run. While fixture owners are still available,
retry intent/storage cleanup using the retained helper:

```sh
/path/to/run/ciacceptance -action cleanup -evidence /path/to/run
```

Under the shared host lock, after fixture daemons are stopped and native stop is
confirmed, run finalization and inspect the verdict:

```sh
flock -w 30 /var/lock/faas-builder-acceptance.lock \
  /path/to/run/ciacceptance -action finalize -evidence /path/to/run
/path/to/run/ciacceptance -action verdict -evidence /path/to/run
```

The helper must be the metal-tagged binary built by the runner. Finalization
repeats the native leak check before removing the owned schema/files; it never
issues VM commands. A failed or interrupted scenario still cannot acquire a
passing verdict merely by completing cleanup. Use a fresh run after recovery.

## Portable checks and current status

```sh
make test-ci-acceptance
go test -tags metal -run '^$' ./tests/ci/acceptance ./scripts/ci/ciacceptance
bash -n scripts/ci/run-native-ci-acceptance.sh
```

These check the harness, ownership journal and compilation. They do not qualify
native execution. This development workspace lacks KVM and configured GCS
credentials; native/GCS execution remains pending. CI admission and dispatcher
are implemented behind default-off flags. See
[ADR-1653](../adr/1653-ci-native-acceptance-harness.md) and
[ADR-1654](../adr/1654-internal-ci-execution-service.md).
