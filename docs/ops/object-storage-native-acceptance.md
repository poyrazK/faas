# Deployed object-storage starter acceptance

`make test-object-storage-native` deploys the exact embedded `object-storage-node`
and `object-storage-python` sources through the normal source builder and exercises
real Firecracker guests. Each starter runs the same eight phases sequentially in
its own disposable catalog, application, bucket, gateway and daemon harness. The
combined run has a 60-minute deadline; neither runtime can substitute for the other.
The harness is implemented; supported native execution and host leak qualification
remain pending. Portable application-hook tests and a successful tagged build do
not establish native acceptance.

The fixture uses ordinary bucket/compute-binding/secret APIs, encrypted catalog
custody, scheduler rotation, guest delivery and application acknowledgments. HTTP
requests traverse the real public and internal application gateways. A private,
owned TLS listener wraps the production SigV4 S3 gateway with the production S3
adapter pointed at an in-memory HTTP provider. It needs no cloud provider account.
Disposable accounting is seeded; lifecycle/adoption/rotation receipts are not.

Only the public fixture certificate is added to the source, at
`runtime/fixture-ca.pem`. The unchanged Dockerfile already copies that directory.
Node's `NODE_EXTRA_CA_CERTS` and Python's `AWS_CA_BUNDLE` select it through
application configuration; TLS hostname and certificate verification stay enabled.
No test-only application routes,
modified handlers, dependency changes or restore-budget overrides are installed.

The strict version-2 verdict requires these phases in order for **each** starter,
an identity emitted after that starter's registered cleanup, and passes for both
starter tests, the root and the package. Missing, skipped, failed, duplicated or
out-of-order journeys cannot qualify. Version-1 Node-only evidence is rejected:

| Phase | Required evidence |
| --- | --- |
| `starter_deploy_and_initial_restore` | Normal source deployment, exact source archive matched to build provenance, deployed OCI app-layer identity, applied manifest, initial snapshot/restore receipt, current generation/version acknowledgments, one upload and exact download bytes. |
| `same_revision_transport_recovery` | Fresh snapshot containing an actual S3 transport; disconnect owned gateway connections; real restore; successful validation on a new TLS connection; unchanged secret versions and object bytes. |
| `running_rotation_and_retirement` | Authenticated public rotation, public completion state, retirement of previous resident guests, old SigV4 authority rejected, new authority and new application write accepted. |
| `parked_rotation_scheduler_recovery` | Park, stop schedd, rotate through the API, retain previous authority until retirement, invalidate the old snapshot, restart schedd and require durable completion and a new write. |
| `rotated_snapshot_restore` | Fresh capture and actual restore using rotated credentials, a fresh validated transport, current acknowledgments and preserved bytes. |
| `failed_hook_cold_boot` | Delay exactly one validation listing beyond the shipped 1.5-second budget; observe cancellation, vmmd's failed-hook acknowledgment, `cold_boot` receipt, stale failed capture and one resident replacement. |
| `public_hook_forbidden` | External POST with platform and spoofed loopback-forwarding headers is forbidden; normal authenticated object reads still work. |
| `revoke_and_independent_cleanup` | Independent provider inventory/byte checks and exactly one provider PUT per explicit upload; public binding deletion; retired key rejected while an independent control key works; control revocation; no active keys or hidden rotation stages; managed secret/token removal; owned objects and bucket deleted; daemons stopped. |

Host observer requests carry a fixture marker solely to exclude them from guest
transport measurements. They still require ordinary valid SigV4 authentication.
The marker never grants authority. Writes are issued once; the harness does not
retry mutations. One observed provider PUT per test write is a bounded acceptance
assertion, not an exactly-once guarantee for application retries.

Run from a clean regular clone on an idle designated x86_64 Linux KVM acceptance
host with root, Firecracker/jailer, a qualified kernel/builder base and current
static guest init:

```sh
export DATABASE_URL='<disposable PostgreSQL catalog administrator URL>'
export FAAS_PGTEST_TEMPLATE_DATABASE=1
export FAAS_TEST_KERNEL=/srv/fc/base/vmlinux-6.1.134
export FAAS_BUILDER_BASE_PATH=/srv/fc/base/runner-builder-amd64.ext4
export FAAS_GUEST_INIT='<static Linux amd64 guest/init built from this checkout>'
export FAAS_PUBLIC_IFACE='<host outward interface>'
export GREGALE_STORAGE_NATIVE_HOST_CLASS=native
export GREGALE_STORAGE_NATIVE_RESULTS_PATH=/private-test-directory/storage-results.jsonl
export GREGALE_STORAGE_NATIVE_REPORT_PATH=/private-test-directory/storage-report.json
sh scripts/test-object-storage-native.sh --preflight
make test-object-storage-native
```

Build guest init with `-buildvcs=true` from that same clean revision. Reused harness
binaries also need matching clean Linux amd64 VCS metadata; the runner forces VCS
stamping for its Go builds. The ordinary reproducible production build disables
stamping and cannot supply these qualification binaries. The shared harness
validates the actual static guest-init executable before starting guests.

The runner refuses a non-designated/busy host, active fleet runtime services,
disabled PostgreSQL, existing evidence paths and an already assigned benchmark
address. It takes `/var/lock/faas-builder-acceptance.lock`, runs `make leakcheck`,
temporarily owns `198.18.0.254/32` on loopback and removes only its own address on
exit. The application declares that `/32` and the owned listener's TCP port through
the ordinary app policy API; no egress denylist is relaxed. Final leak checks and
source/artifact revalidation must pass before a report is written. Private JSON
logs are retained on failure; explicit results/report paths must be distinct and
must not already exist. `--preflight` starts no daemons or guests and takes no
ownership of the address; it does not certify later host availability.

The version-2 report contains a `journeys` entry for each starter with its template,
runtime label, eight phase results and cleanup identity. Each identity binds the
clean revision, acceptance executable/daemon/helper hashes, guest init, kernel,
builder base, Firecracker/jailer, shared minimal `deploy_base` bytes and that
starter's actual deployed app layer and exact source archive (`node22_app_layer`
and `node22_source_archive`, or `python312_app_layer` and `python312_source_archive`).
The runtime label identifies the shipped Dockerfile's declared language family;
these full OCI apps carry their interpreters in drive1 and do not publish function
runtime releases. The source hash must match both deployment and builder provenance;
the successful source build must have a pinned OCI image digest. Both identities
must agree on the revision and every shared artifact hash. Application and source
hashes must differ between starters. Node identity or renamed Node artifact evidence
cannot qualify Python.
The verifier reassembles Go's fragmented identity output with a 16 KiB limit;
truncated, interrupted or duplicated identity logs cannot qualify.
The report contains phase results and log/evidence hashes, never raw logs, credential
material or object contents, and expires after 24 hours. Hashes bind evidence;
operator custody and a trusted host are still required. The report does not enable
any feature or change a rollout gate.

Use `GREGALE_STORAGE_NATIVE_HOST_CLASS=nested-diagnostic` only for diagnostics.
Such a report always has `native_qualified=false`. Native acceptance is not
supported in nested virtualization. Live-provider durability/rotation behavior,
public DNS/Internet reachability and supported native execution of both starters
remain pending qualification work. Revoked credential audit rows follow ordinary
catalog retention; cleanup proves authority retirement and removal of managed
runtime-secret rows, not erasure of that audit history.

Portable checks, also wired into CI:

```sh
python3 scripts/ci/object-storage-native-verdict_test.py
python3 scripts/ci/object-storage-native-runner_test.py
sh -n scripts/test-object-storage-native.sh
go test -race ./pkg/e2etest/storagestarter ./pkg/e2etest/storagefixture
go test -race ./pkg/e2etest -run '^Test(StorageGuest(Manifest|App)|PostgresGuestManifest|PostgresGuestCachedBinary)'
go test -race ./pkg/s3gateway -run '^TestStorageNativeIndependentObserver$'
go test -race -tags metal,object_storage_native -c -o /tmp/storage-native.test ./cmd/e2e
```
