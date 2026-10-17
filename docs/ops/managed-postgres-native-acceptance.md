# Deployed PostgreSQL acceptance

`make test-managed-postgres-native` exercises the ordinary customer source
deployment with a real SQL application and real Firecracker guests. It complements
the backend-specific v8 durable qualification; it does not replace that gate or
enable provisioning.

The runner selects `metal,managed_postgres_native`. The additional tag keeps
this isolated TLS/SCRAM and proxy acceptance outside the ordinary metal suite,
which uses different database prerequisites. Compiling this test and invoking
it without the strict runner's opt-in fails; it never silently skips.

The disposable fixture runs PostgreSQL with TLS and SCRAM, in a separate empty
customer database. Provider management is simulated through an authenticated loopback fixture. The
real apid daemon loads this driver only when compiled with both acceptance tags;
production builds (including ordinary `metal` builds) register Neon alone. The
fixture uses the real
managed database and binding services, catalog, age/HMAC credential delivery,
release task, scheduler, vmmd, imaged, builderd and gateway. It needs no Neon key or
paid provider resources.

The strict verdict requires all nineteen phases and the parent test to pass:

1. Upload a scratch Dockerfile containing the SQL probe compiled from the current
   checkout; run its normal release migration; restore the initial serving guest
   and read/write the migration's marker through independently restricted roles.
2. Run an ordinary application task that fails if a migration credential is
   delivered to it. The serving probe also rejects that credential, including
   readiness checks.
3. Rotate the writer while running. Require the real scheduler's retirement
   receipt, withdrawal of previous resident guests, stale snapshot invalidation,
   and a PostgreSQL authentication rejection for the retired credential.
4. Park a guest retaining writer and reader pools, then disconnect its external
   SQL sessions. Require a real snapshot restore and successful loopback-only
   `after_restore` callback that resets both pools and reads the existing marker
   through new TLS sessions before readiness. Preserve data and credentials.
5. Park and restore a guest using the new credential. Require the gateway's
   `restore` receipt, the new credential proof, unchanged reader proof and marker,
   and increasing SQL counter.
6. Stop schedd before rotating the parked workload through the public API, then
   restart it. Require durable notification recovery,
   retirement, delivery of the next credential and preserved SQL data.
7. Upload the unchanged embedded `rest-api-postgres` source, lockfile and manifest.
   Add only the disposable server's public CA under `runtime/fixture-ca.pem` and a
   nonsecret build token. The unchanged Dockerfile already copies `runtime/`;
   `NODE_EXTRA_CA_CERTS=/app/runtime/fixture-ca.pem` selects trust for both release
   and serving processes, with certificate and hostname verification enabled. Run
   its ordinary `node migrate.js` release, restore through `/healthz`, and insert
   exactly one note through the shipped HTTP handler.
8. Run an ordinary Node application task and require a runtime credential while
   rejecting the migration credential. Serving startup also rejects that secret.
9. Read to retain a real `pg` pool, capture a fresh snapshot, disconnect external
   SQL sessions, and require the gateway's `restore` receipt with the same note.
   A forwarded external request to the lifecycle path must be forbidden.
10. Capture again and delay the next SQL connection by 2.5 seconds: beyond the
    starter's 1.5-second recovery budget. The one-shot fault is consumed by
    restore, so SQL is healthy for the fresh cold boot.
    Require vmmd's failed-hook acknowledgment, a `cold_boot` wake receipt, stale
    failed snapshot, one resident replacement guest, and unchanged note data.
11. Rotate the running starter through the public API, await the scheduler and
    public completion state, reject old SQL authentication, preserve the original
    note and perform a new write using the replacement credential.
12. Capture the replacement process even when fallback/rotation retired every
    earlier snapshot. Stop schedd before rotating the parked starter through the public API, then
    restart it. Require
    recovered retirement/completion, preserved data and another successful write. An independent SQL read
    confirms the complete notes dataset was neither lost nor replayed.
13. Deploy the unchanged `rest-api-postgres-python` source, requirements and
    manifest, with the same public CA under `runtime/fixture-ca.pem` and a
    nonsecret build token. Set `SSL_CERT_FILE=/app/runtime/fixture-ca.pem` for
    release and serving processes. Its pinned libpq driver uses explicit system
    trust with `verify-full`; managed connection URIs are unchanged. Require its
    ordinary `python migrate.py` release, initial snapshot restore, preservation
    of all Node notes and one new Python HTTP write.
14. Run an ordinary Python application task and require the runtime credential
    while rejecting the migration credential. Serving startup also rejects it.
15. Retain the shipped `psycopg_pool` pool, capture a fresh snapshot, disconnect
    external SQL sessions and require a real restore with every existing note.
    Require external lifecycle requests to be forbidden.
16. Delay the next SQL connection beyond the recovery budget and require a
    failed-hook acknowledgment, cold-boot receipt, stale failed snapshot and one
    resident replacement, preserving the complete notes dataset.
17. Rotate the running Python starter through the public API. Require scheduler
    retirement and public completion, reject retired SQL authentication and
    preserve all data while adding one write under the new credential.
18. Park the replacement Python process, stop schedd before rotation and restart
    it afterward. Require durable recovery, retirement/completion, preserved
    data and one new write. Independently read every note and its total count
    through SQL to catch loss, substitution or replay across both starters.
19. Stop owned daemons, disable fixture provisioning, delete every owned binding,
   independently prove encrypted-secret removal and SQL credential retirement,
   then delete the managed database. Cleanup failures fail the parent test;
   role/database teardown and host leak checks are also required.

All three workloads rotate through the authenticated `POST /v1/postgres/bindings/{id}/rotate`
route and poll the public binding state. They never update application restart
status, send scheduler notifications, fabricate retirement receipts, or invoke
rotation reconciliation themselves. The production API and binding worker own
those steps. The separate SQL-only regression suite has no guests and explicitly
supplies its retirement receipt.

A portable tagged API test also exercises the production loader, authentication,
sealing, binding worker, held-session revocation and restart persistence against
this same disposable SQL fixture. Both CI workflows explicitly invoke it with
`metal,managed_postgres_native`; it does not claim guest execution.

The Go probe retains separate writer and reader pools. The customer Node and Python
starters retain their shipped writer pools; their source and handlers have no
test-only routes. Each source manifest opts into a two-second `after_restore`
callback; the handler requires a
loopback peer and the platform header. It discards captured sockets and reads
the existing marker without mutations. Missing data or unavailable SQL fails
the callback and readiness. Manual tasks still use fresh short-lived sessions.
The proxy retires sessions only through explicit fault controls or cleanup;
it does not expire idle connections after thirty seconds.

Every successful starter read also waits for current public writer-binding
adoption evidence: complete inventory, no pending rotation, the current
credential generation, and matching secret-version/process-generation ACKs for
every resident in the current deployment. Readiness alone cannot satisfy this
check. Both starters share the notes dataset, and an independent SQL check after
each journey verifies all expected IDs and bodies without replaying mutations.

The local TLS/SCRAM race tests exercise retained sessions beyond that former
deadline, socket loss, concurrent writes, pre-commit cancellation, hook failure,
and retirement of held sessions and fresh logins. Mutations use explicit
transactions and are never automatically retried. A failed COMMIT can still
have an uncertain outcome; these checks do not establish exactly-once delivery
for application retries.

The deployed runner must actually pass to establish guest pool/hook acceptance.
Version-1/version-2 reports and partial Node-only reports fail the current verdict.
Compilation and local SQL tests alone cannot do so. This fixture does not qualify
provider pooling, real-provider DNS/Internet access, branching or point-in-time
recovery: its pooled/direct endpoints share one SQL server. Fresh backend-specific
qualification and supported native-host execution remain separate requirements.

Run on an idle designated x86_64 Linux acceptance host with root, KVM, Firecracker,
jailer, ip, nft, Python 3, a supported guest kernel and qualified builder base:

```sh
export DATABASE_URL='<private disposable PostgreSQL administrator URL>'
export FAAS_PGTEST_TEMPLATE_DATABASE=1
export GREGALE_POSTGRES_NATIVE_CA_PATH='<public SQL certificate/CA; server SAN must include 198.18.0.254>'
export FAAS_TEST_KERNEL=/srv/fc/base/vmlinux-6.1.134
export FAAS_BUILDER_BASE_PATH=/srv/fc/base/runner-builder-amd64.ext4
export FAAS_GUEST_INIT='<static Linux amd64 guest/init binary built from this checkout>'
export FAAS_PUBLIC_IFACE='<host outward interface>'
export GREGALE_POSTGRES_NATIVE_HOST_CLASS=native
export GREGALE_POSTGRES_NATIVE_RESULTS_PATH=/private-test-directory/results.jsonl
export GREGALE_POSTGRES_NATIVE_REPORT_PATH=/private-test-directory/guest-report.json
make test-managed-postgres-native
```

Use `nested-diagnostic` on the internal GCE node. Such a run is diagnostic evidence
and cannot qualify production rollout. The runner refuses a virtual machine
declared `native`, non-designated or busy hosts, active fleet runtime services,
disabled PostgreSQL tests, missing fixtures and skipped/missing phases. Keep
private result logs until failure diagnosis is complete. The guest init must be
the real static Go executable: scripts, unrelated binaries and dynamic loaders
are rejected before imaged can stage a base. With a guest kernel configured,
the harness refuses to substitute its non-guest placeholder PID 1.

The runner holds the shared acceptance lock and temporarily assigns
`198.18.0.254/32` to loopback for a TLS passthrough proxy. The reserved benchmark
address lets the guest reach this disposable SQL fixture. The Pro app declares
TCP 5432 and the proxy's single `/32` destination through the ordinary public
app API. DNS-gated egress requires this explicit CIDR for a literal IP; no
resolver receipt is fabricated and the host and guest IP denylists are unchanged.
It refuses an existing address, removes its own address on
exit, and checks guest resource leaks before and after the test. It never modifies
the production PostgreSQL server's configuration or billing plan.

## Combined rollout verification

The runner requires a clean checkout and matching clean Linux amd64 VCS metadata
on its Go daemons, test executable and guest init. Reused harness binaries from another revision
are rejected. It fingerprints the actual execution bundle, the shared minimal
drive0 base, and each workload's deployed drive1 app layer and exact source
archive. All three workloads are full OCI apps; function-runtime releases do not
describe these deployments. Each source hash must match the deployment and
its successful build provenance, with the OCI output digest pinned. Source
archives are retained for the whole journey, the shared minimal base must stay
unchanged between workloads, and all files are rehashed after workload shutdown.
The runner allows 45 minutes and emits a version-3 guest report only after all
test, SQL and host cleanup succeeds.
The report expires after 24 hours. Its default path is alongside the private
JSON log; explicit result/report paths must not already exist.

Approve the expected qualification-bundle manifest independently. Its format is
`version: 3`, `source_revision: <40 lowercase hex characters>` and `artifacts`,
a map of named SHA-256 values. The complete key list lives in
`pkg/managedpostgres/guestqualification/contract.json`. It includes test-tagged
daemons, guest init, kernel, builder base, host executables, the test executable,
`deploy_base`, `probe_app_layer`, `probe_source_archive`, `node22_app_layer` and
`node22_source_archive`, `python312_app_layer` and `python312_source_archive`.
The three app-layer hashes must differ, as must the three source-archive hashes;
one workload's bytes cannot qualify another. Check the expected
workload artifacts independently against the deployment and build records before
approving them. Version-1/version-2 reports and manifests cannot supply combined
readiness and must be regenerated. These test binary
hashes are not production daemon hashes; approve their source revision and
qualification build together with the intended release.

The verdict requires all nineteen phases once, in contract order, one cleanup
identity after all phases and before the parent pass, and a final package pass.
It reassembles Go test2json's fragmented identity log lines with a 16 KiB bound;
truncated, interrupted, duplicate or oversized identities are rejected. Reports
contain hashes and bounded metadata, never raw output or SQL credentials.

Use the existing verifier without any provider/SQL/VM calls:

```sh
FAAS_ENVIRONMENT=staging \
FAAS_MANAGED_POSTGRES_CONFIG=/private-test-directory/provider-config.json \
managed-postgres-qualify --verify --rollout \
  --approval /private-test-directory/provider-v8.json \
  --guest-report /private-test-directory/guest-report.json \
  --runtime-manifest /private-test-directory/approved-bundle.json
```

The combined result separates `provider_lifecycle` and `guest` readiness. Both
must pass, including the same PostgreSQL major, runtime identity and unexpired
evidence. Fresh provider v8 output must contain clean executable-source
provenance matching the bundle revision; older unstamped provider reports stay
diagnostic even when their configuration fingerprint matches. Build the
qualifier and native bundle from the same clean checkout with Go VCS stamping
enabled. Use a regular clone with a `.git` directory: the pinned Go toolchain
does not discover the `.git` file used by Git worktrees. Build the qualifier and
guest init with `-buildvcs=true`; the runner forces this flag for the test and
harness builds. The general `make build` disables VCS stamping for reproducible
production builds and cannot supply these qualification executables. Confirm
`go version -m` lists the intended `vcs.revision` and `vcs.modified=false` before
using prebuilt files. A nested diagnostic report never supplies native readiness. Digests
detect inconsistent evidence; operator custody and a trusted test host remain
necessary because these documents are not signed remote attestation.

Before a staging canary, configure the provider approval plus
`FAAS_MANAGED_POSTGRES_REQUIRE_GUEST_QUALIFICATION=true`,
`FAAS_MANAGED_POSTGRES_GUEST_QUALIFICATION_PATH` and
`FAAS_MANAGED_POSTGRES_RUNTIME_MANIFEST_PATH` on apid. Providing either guest
path opts in even with a false boolean. Missing/malformed inputs, wrong bundles
and expired reports keep the staging gate closed; legacy environment approval
cannot bypass it. Restart after replacing files. This policy does not open
production admission. See [ADR-992](../adr/992-managed-postgres-combined-rollout-readiness.md)
the provenance/report correction in [ADR-1379](../adr/1379-postgres-native-acceptance-repair.md),
and the Python extension in [ADR-1380](../adr/1380-python-postgres-native-acceptance.md).
