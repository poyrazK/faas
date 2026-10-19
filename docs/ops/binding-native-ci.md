# Native binding qualification lane

The `bindings` lane of `e2e-native.yml` runs the existing strict PostgreSQL,
object-storage, queue and managed HTTPS service guest journeys serially. It is manual and main-only,
shares the configured acceptance node's workflow concurrency group, and uses
each runner's existing host lock, service checks and before/after leak checks.
It does not change provider admission, IAM, production configuration or release
approval. The general `full` lane covers the ordinary `metal` test set; these
four journeys require their additional build tags and fixture contracts.

## Host prerequisites

Use the existing designated idle native Linux amd64 acceptance host. The lane
requires root, readable KVM, `/etc/faas/builder-acceptance-host`, and successful
VM **and** container detection establishing a native host. A virtual machine
with nested KVM cannot qualify. If the configured host is virtual, unavailable,
or missing fixtures, the lane fails; it never provisions a replacement or
falls back to a diagnostic pass. Host changes follow the normal operator
approval process.

No serving runtime daemon or Firecracker workload may be active. The lane
refuses these conditions rather than stopping customer workloads. Install the
dependencies and approved runtime artifacts required by the existing
[PostgreSQL](managed-postgres-native-acceptance.md),
[storage](object-storage-native-acceptance.md),
[queue](queue-bindings-native-acceptance.md) and
[managed HTTPS](managed-service-binding-native-acceptance.md) runbooks.

Configure `/etc/faas/bindings-native.env` as a regular, root-owned mode-0600
shell environment file. It must provide:

| Variable | Requirement |
| --- | --- |
| `DATABASE_URL` | Disposable PostgreSQL catalog with the privileges required by the native fixtures, TLS and SCRAM; never a production catalog |
| `GREGALE_POSTGRES_NATIVE_CA_PATH` | Fixture SQL CA/certificate with `198.18.0.254` in its SAN, as required by the PostgreSQL journey |
| `FAAS_TEST_KERNEL` | Readable approved guest kernel |
| `FAAS_TEST_MINIMAL_BASE_PATH` | Approved generic deployment base and readable `.digest` sidecar for the managed HTTPS fixture |
| `FAAS_BUILDER_BASE_PATH` | Readable approved local builder base used by the existing provenance checks |
| Storage/runtime configuration | The approved artifact backend, published immutable Node runtime base and outward NIC required by the existing journeys |

Quote shell values containing `&` or `#`. Credentials remain on the host; they
are not workflow inputs, SSH arguments or uploaded artifacts. Host class is
always `native` for this lane. `FAAS_SKIP_PG_TESTS` is rejected.

## Dispatch and source custody

After merging the lane implementation:

```sh
gh workflow run e2e-native.yml --ref main \
  -f lane=bindings -f keep_node_running=false
```

CI transfers a fresh, self-contained Git clone of the exact dispatch SHA and
the pinned Go toolchain. The clone has a regular `.git` directory and does not
inherit checkout-action credentials, hooks or dirty files. Guest init and
harness binaries retain clean matching VCS provenance; no archive placeholder
or reproducible production binary substitutes for qualification binaries.

The source tree remains clean. Outputs and Go caches live outside it. The
remote unit is bounded to 270 minutes, inside a 300-minute job budget that
leaves time for staging, evidence collection and shutdown. Each journey retains
its original test alarm. The default stops the acceptance node on failure,
cancellation and success; `keep_node_running=true` is the existing explicit
debugging opt-in.

## Evidence

`scripts/ci/binding-native-suites.json` records the four required runner and
contract identities. The aggregate replays each runner's existing strict
verdict against its private Go JSON log. Missing, skipped, failed, reordered or
incomplete phases and cleanup cannot supply a summary. Reports must match the
same source SHA, share the actual kernel, builder base, guest init, Firecracker
and jailer hashes, be fresh, and describe serial non-overlapping runs.

The uploaded artifact contains the four bounded reports and `summary.json`,
with report hashes and the earliest expiry. Summary version 2 requires all four
suites; the earlier three-suite inventory cannot qualify the current lane. Raw
guest/daemon output never enters Actions artifacts. Successful runs finalize and remove their host bundle
after collection. Failed runs leave the registered bundle for private diagnosis
until the existing abandoned-run lease makes it eligible for the normal
retention sweep (default: six hours from registration). Invalid reports are not
copied into public artifacts. Inspect private logs through the existing
artifact-custody/runbook process; retention in Actions does not extend
qualification expiry.

`native_guest_qualified=true` means all four guest journeys and cleanup passed.
`release_qualified` remains false. Provider lifecycle qualification and
independent review of expected artifact manifests remain separate requirements;
these hashes are not remote attestation or permission to enable production.

## Portable PR checks

```sh
go test -race ./scripts/ci/nativetestinventory
go run ./scripts/ci/nativetestinventory --check
bash scripts/ci/native-e2e-phases_test.sh
python3 scripts/ci/binding-native-lane_test.py
python3 scripts/ci/binding-native-host_test.py
python3 scripts/ci/managed-service-native-verdict_test.py
python3 scripts/ci/managed-service-native-runner_test.py
```

Go's build constraints and AST determine the Linux amd64 test inventory. Plain
metal tests must belong to a platform phase, and specialized tests must match
their strict runner contracts. A new unowned extra-tag test fails the portable
coverage check. These checks run in PR CI and do not claim native execution.
