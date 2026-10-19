# Managed HTTPS service binding native acceptance

`make test-managed-service-native` runs a self-contained managed HTTPS journey
on the designated idle Linux x86_64 KVM acceptance host. It is the fourth suite
of the [binding-native CI lane](binding-native-ci.md). It creates its own
control-plane daemons, disposable catalog schema, private local artifact store, reviewed-source/OCI
fixtures, account, private certificates and three-workload `acceptance` graph
(HTTP caller, billing target and queue worker).
An undeployed standalone app supplies the service-policy control.
No pre-existing API endpoint, customer token or prepared graph is needed.
Never run it on serving compute nodes.

## Prerequisites and invocation

Use the existing approved host, exact clean Git checkout and pinned Go toolchain.
The runner requires root, readable KVM, `/etc/faas/builder-acceptance-host`,
Firecracker, jailer, network tools and an idle host. It refuses active serving
daemons, a dirty checkout, skipped PostgreSQL tests and existing output files.
Native qualification requires both VM and container detection to establish a
native host; `nested-diagnostic` can only produce diagnostic evidence.

Provide the existing fixture settings through the host's private configuration:

| Variable | Requirement |
| --- | --- |
| `DATABASE_URL` | Disposable PostgreSQL catalog with schema-creation privileges |
| `FAAS_TEST_KERNEL` | Approved kernel for the installed Firecracker version |
| `FAAS_BUILDER_BASE_PATH` | Approved builder base used by the shared harness |
| `FAAS_TEST_MINIMAL_BASE_PATH` | Approved generic deployment base with its `.digest` sidecar |
| `FAAS_GUEST_INIT` | Real static guest init built from this exact clean checkout, with VCS metadata |
| `GREGALE_SERVICE_NATIVE_HOST_CLASS` | Explicit `native` or `nested-diagnostic` |

The aggregate CI entrypoint builds guest init itself. For a standalone run,
build it outside the checkout and use new, separate report/log paths:

```sh
GOOS=linux GOARCH=amd64 CGO_ENABLED=0 go build -trimpath -buildvcs=true \
  -tags linux -o "$PRIVATE_RUN_DIR/guest-init" ./guest/init
export FAAS_GUEST_INIT="$PRIVATE_RUN_DIR/guest-init"
export GREGALE_SERVICE_NATIVE_HOST_CLASS=native
export GREGALE_SERVICE_NATIVE_RESULTS_PATH="$PRIVATE_RUN_DIR/service.jsonl"
export GREGALE_SERVICE_NATIVE_REPORT_PATH="$PRIVATE_RUN_DIR/service.report.json"
make test-managed-service-native
```

`PRIVATE_RUN_DIR` must be a newly created private directory outside the source
checkout. Host/provider configuration stays private. The runner holds the
existing native-host lock, runs `make leakcheck` before and after the test, and
uses a 60-minute test alarm. `--preflight` checks prerequisites without builds,
daemons, guests or network mutation.

## Required journey

The strict contract is
[`servicestarter/contract.json`](../../pkg/e2etest/servicestarter/contract.json).
Every phase must run and pass in order:

1. Review, approve and adopt the caller/billing/worker graph through the normal GitOps
   APIs. Wait for actual capture, restore, smoke, activation and serving evidence.
   The frozen HTTP and worker callers use HTTPS with custom `BILLING_ENDPOINT`
   and `WORKER_BILLING_ENDPOINT` environment keys. The worker's reviewed push
   queue has an explicit bounded qualification smoke.
   After each activation, explicitly publish and complete one owned serving
   message through normal queue admission before measuring probe isolation.
2. Run the exact-source CLI's single-service verification. Require the real
   guest's verified TLS and exact target/release confirmation, then refresh the
   authoritative inventory and match the durable task receipt.
3. Run `--all --require-complete` and require complete one-edge coverage.
   Both CLI forms must create exactly one probe and cause no application
   invocation or serving-instance wake.
4. Enqueue a genuine future-due fixture message, then verify the worker's single
   HTTPS binding. Require an unchanged queue identity, payload, deadline and
   attempt count. Run a separate ordinary task that rejects a worker startup
   marker or live listener; a restored worker snapshot cannot pass this check.
   Neither task may enter serving-instance inventory or change queue work.
5. Run worker `--all --require-complete --queue-publish`. The reviewed fixture
   declares safe handling. Require complete HTTPS and queue coverage, exactly one
   new bounded queue invocation, and an exact deployment/release handler-completion
   receipt. Preserve the unrelated pending message and reject any additional work.
   The queue delivery may wake its worker; isolated HTTPS tasks must not do so.
6. Reject a conflicting target UUID before task creation. Reject an attempted
   caller-supplied internal payload naming a different release; public callers
   cannot appoint the server-selected release context.
7. Review and qualify a changed target image. Reject the prior release's HTTP
   and worker receipts and admission, including completed queue evidence, then require fresh HTTPS and queue evidence.
8. Change the owned standalone service-policy control through its owner API.
   The deliberately account-wide service dependency token must invalidate the
   old managed receipt while release and caller/target deployments stay fixed.
   Require fresh guest evidence. Also revise the caller's binding release policy,
   reject an old durable verification run and require a newly reviewed run to pass.
9. Independently reject the worker's old policy receipt with unchanged release
   and deployment IDs, then require a fresh successful worker service probe.
10. Stop only the owned scheduler, admit and cancel a queued task, restart it and
   require that cancelled work cannot pass. Also reject completion of a cancelled
   durable verification run after genuine guest evidence exists.
11. Observe an original durable deadline expire, reject completion, and reject
   the real guest receipt under a one-second freshness limit. No SQL timestamp
   edits or fabricated expired tasks supply this case.
12. Independently inventory every HTTP and worker task, cancel owned durable
   authority, and review an empty graph with both `workload_pruning_policy` and
   `queue_pruning_policy` set to `retain`. Keep binding release policies enforced
   through the checked removal described in [ADR-1647](../adr/1647-checked-binding-retirement.md). Wait
   for all three workloads to retire and confirm the future-due message stays
   pending before
   stopping daemons. Native recovery preserves live guests on daemon exit, so
   shutdown alone is insufficient cleanup. Rehash all recorded artifacts.

The private CA has a critical `.internal` DNS name constraint. VMMD stages only
its public trust anchor; the gateway serves a private `*.internal` leaf on the
host bridge. Neither certificate validation nor normal admission is bypassed.
The local OCI/source providers and the private base scan fixture isolate the
journey from external providers; they do not qualify provider lifecycle or a
production image scan.

## Evidence and limits

The runner accepts only the complete ordered Go JSON execution, successful
parent/package results, final cleanup identity and independent leak checks.
Missing, skipped, duplicated, reordered or failed phases cannot qualify.
Reports contain bounded stage results, times, source SHA and artifact hashes;
private keys, account tokens, connection strings and raw logs stay on the host.
The manifest covers the actual daemon, guest, CLI and test binaries, runtime
assets, deployed layers, reviewed definitions, policy request and public CA. It preserves earlier
hashes and rechecks them after teardown. Existing evidence is never overwritten.

The aggregate's version 2 requires this suite as well as PostgreSQL, storage and
queue. The managed-service suite's version 4 requires all thirteen phases and the
worker layer identities, complete worker queue delivery and stale queue evidence rejection. Older three-suite, HTTP-only or queue-incomplete service evidence cannot
qualify the current lane.
`release_qualified` remains false: provider qualification and independent
approval of expected artifacts are still required. A portable test, tagged
compile or skipped general metal test supplies no native qualification.

This implementation workspace has no `/dev/kvm`; real native execution and
host leak qualification remain pending. See
[ADR-1642](../adr/1642-managed-https-service-probes.md) and
[ADR-1643](../adr/1643-managed-worker-service-probes.md) and
[ADR-1645](../adr/1645-reviewed-source-binding-verification.md).

The source-build phase switches the reviewed worker to a Dockerfile from the
immutable Git archive and uses the real builder microVM pipeline. It requires
an actual builder base, a succeeded build, matching archive checksum/size and
frozen provenance, then runs strict HTTPS and push-queue CLI verification. The
subsequent release replacement rebuilds that source worker and revokes its old
HTTPS and queue proof. Version 4 retains both archives, provenance records and
built layers; earlier image-only evidence cannot qualify this contract.

The service runner allows 90 minutes for both source builds plus the existing
qualification/probe/cleanup journey. Each source activation includes the normal
15-minute build ceiling in its wait budget. The aggregate remote cap is 270
minutes within the existing 300-minute job, preserving collection and node-stop
allowance.

Queue revocation is checked immediately after the source approval advances,
while the prior bounded completion receipt is still unexpired, before waiting
for a potentially 15-minute source build. Expiry cannot stand in for context
revocation. The unrelated pending sentinel is due after two hours, beyond the
90-minute suite alarm, and retains its exact original payload and attempt state.
