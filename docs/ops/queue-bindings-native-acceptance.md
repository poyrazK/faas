# Deployed queue binding acceptance

`make test-queue-bindings-native` deploys the exact embedded Node `queue-worker`
through the normal source builder and SDK uploader. It exercises public canary,
durable verification and promotion APIs against real Firecracker guests. The
harness is implemented; supported native execution and host leak qualification
remain pending. Compilation and portable fixtures do not establish acceptance.

The starter handles the reserved canary on a safe no-op path before ordinary
jobs. The exact deployment explicitly declares `GREGALE_QUEUE_CANARY_BINDINGS`
in its `override_env`; publication independently requires consent. Malformed or
undeclared probes fail. Customer intent cannot certify ledger membership or
replace the platform's exact-deployment, attempt, replay and lease fences.

The version-1 contract requires these nine phases in order:

| Phase | Required evidence |
| --- | --- |
| `starter_deploy_and_park` | Exact shipped source, successful build provenance, published Node function runtime, applied capability override, real snapshot and no resident guest. |
| `consent_and_review_without_publication` | Healthy scheduler observations, side-effect-free server review, rejected publication and receipt creation without consent, unchanged invocation count and blocked promotion. |
| `parked_delivery_and_release_qualification` | Real restore receipt, successful handler delivery/completion timestamps, one original attempt, no replay, selected deployment and public promotion qualification. |
| `scheduler_restart_receipt_resume` | Stop schedd before admission, discard its response, resume the original durable receipt and resource, retain original expiry, re-admit without another invocation, restart schedd and require completion plus final receipt qualification. |
| `cancelled_evidence_rejected` | Public cancellation removes completion authority and fresh promotion fails without changing routing. |
| `expired_evidence_rejected` | Real successful completion before the original short deadline, observed expiry and blocked promotion afterward. No timestamp mutation. |
| `completed_replay_rejected` | Public dead-letter replay rejects completed work; invocation count is unchanged. Cancellation then blocks promotion. |
| `wrong_deployment_evidence_rejected` | A second source deployment at zero traffic cannot qualify with fresh completion on the still-serving first revision, generated after the candidate build. Fresh exact-candidate completion permits its promotion. |
| `independent_cleanup` | Independent invocation inventory matches owned canaries, every canary is cancelled, binding/consumer retired, daemons stopped and all runtime/source artifacts rehashed. |

The journey uses a disposable migrated PostgreSQL catalog. Complete binding
inventory and promotion fences also initialize the ordinary managed PostgreSQL
catalog service with an unused backend. Its credential is a fixture placeholder;
database provisioning, usage and health collection are disabled and no database
resources are created. No provider account or real provider credential is needed.
The API, scheduler, gateways, builder, guest handler and durable acknowledgement
path remain real. Lifecycle, poll and completion receipts are never manufactured.

Run from a clean regular clone on an idle designated x86_64 Linux KVM acceptance
host with root, Firecracker/jailer, the qualified builder base and kernel, current
static guest init and access to the configured Node function runtime images:

```sh
export DATABASE_URL='<disposable PostgreSQL catalog administrator URL>'
export FAAS_PGTEST_TEMPLATE_DATABASE=1
export FAAS_TEST_KERNEL=/srv/fc/base/vmlinux-6.1.134
export FAAS_BUILDER_BASE_PATH=/srv/fc/base/runner-builder-amd64.ext4
export FAAS_GUEST_INIT='<static Linux amd64 guest/init from this checkout>'
export GREGALE_QUEUE_NATIVE_HOST_CLASS=native
export GREGALE_QUEUE_NATIVE_RESULTS_PATH=/private-test-directory/queue-results.jsonl
export GREGALE_QUEUE_NATIVE_REPORT_PATH=/private-test-directory/queue-report.json
sh scripts/test-queue-bindings-native.sh --preflight
make test-queue-bindings-native
```

Build guest init and reused Go binaries from that same clean revision with VCS
stamping enabled. The runner forces `-buildvcs=true` for its test/harness builds.
The ordinary reproducible production build disables stamping and cannot supply
these qualification executables. The harness validates the static guest-init
executable and refuses a placeholder.

The runner requires `/etc/faas/builder-acceptance-host`, refuses active fleet
runtime services and takes `/var/lock/faas-builder-acceptance.lock`. It runs
`make leakcheck` before and after the 40-minute journey and rechecks source
cleanliness/revision. `--preflight` starts no daemons or guests. Existing result
or report files and symlinks are preserved; results and report need separate paths.
Private logs remain available on failure. No host benchmark address is needed.

The report contains nine checks, timestamps, source/runtime/artifact hashes and
bounded host metadata. It never contains raw guest output or credentials, and
expires after 24 hours. Its artifact key list lives in
`pkg/e2etest/queuestarter/contract.json`. The function uses a published immutable
`node22_base`, both deployed application layers and one exact `source_archive`;
the build and runtime release must agree on the pinned base reference, base bytes
and guest-init hash. Source and shared base bytes must remain unchanged between
deployments. Expected artifact identities still require independent operator
review; hashes are not remote attestation.

Missing, skipped, failed, duplicate, reordered or overlapping phases, malformed
or partial identities, foreign artifacts and incomplete cleanup cannot qualify.
Go test2json identity fragments are reassembled with a 16 KiB limit. The identity
is emitted only after registered cleanups pass. Use `nested-diagnostic` solely
for diagnostics; its report always has `native_qualified=false`.

This proves safe handler completion, not business side effects or exactly-once
delivery. Completed work cannot enter dead-letter replay; portable authority
regressions separately reject evidence after an actual replay-generation change.
Public/provider networking, other runtimes and project graph qualification are
separate work. This runner changes no rollout or provisioning policy. See
[ADR-1381](../adr/1381-queue-worker-completion-acceptance.md).

Portable checks, also wired into CI:

```sh
go test -race ./cmd/gregale/templates -run '^TestQueueWorkerStarter'
go test -race ./pkg/e2etest/queuestarter
go test -race ./pkg/e2etest -run '^TestQueueGuest'
python3 scripts/ci/queue-bindings-native-verdict_test.py
python3 scripts/ci/queue-bindings-native-runner_test.py
sh -n scripts/test-queue-bindings-native.sh
go test -race -tags metal,queue_binding_native -c -o /tmp/queue-native.test ./cmd/e2e
```
