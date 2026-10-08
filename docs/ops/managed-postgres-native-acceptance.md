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
customer database. Provider management is simulated in-process. It uses the real
managed database and binding services, catalog, age/HMAC credential delivery,
release task, scheduler, vmmd, imaged, builderd and gateway. It needs no Neon key or
paid provider resources.

The strict verdict requires all six phases and the parent test to pass:

1. Upload a scratch Dockerfile containing the SQL probe compiled from the current
   checkout; run its normal release migration; restore the initial serving guest
   and read/write the migration's marker through independently restricted roles.
2. Run an ordinary application task that fails if a migration credential is
   delivered to it. The serving probe also rejects that credential, including
   readiness checks.
3. Rotate the writer while running. Require the real scheduler's retirement
   receipt, withdrawal of previous resident guests, stale snapshot invalidation,
   and a PostgreSQL authentication rejection for the retired credential.
4. Park and restore a guest using the new credential. Require the gateway's
   `restore` receipt, the new credential proof, unchanged reader proof and marker,
   and increasing SQL counter.
5. Rotate while parked and restart schedd. Require durable notification recovery,
   retirement, delivery of the next credential and preserved SQL data.
6. Stop owned daemons, disable fixture provisioning, delete every owned binding,
   independently prove encrypted-secret removal and SQL credential retirement,
   then delete the managed database. Cleanup failures fail the parent test;
   role/database teardown and host leak checks are also required.

Rotation uses the production durable runtime-refresh seam after the binding
service updates its credential. The test does not call the public rotation API or
a vendor API and never supplies a scheduler retirement receipt itself. The
SQL-only regression suite explicitly supplies that receipt because it has no
guests; it runs in CI's TLS-enabled PostgreSQL shard.

Every SQL request opens fresh TLS sessions. This suite does not qualify retained
connection pools, application `after_restore` reconnection hooks, provider pooling,
real-provider DNS/Internet access, branching or point-in-time recovery. The fixture
represents pooled/direct endpoints with the same SQL server. Backend-specific
qualification and application reconnect testing remain separate requirements.

Run on an idle designated x86_64 Linux acceptance host with root, KVM, Firecracker,
jailer, ip, nft, Python 3, a supported guest kernel and qualified builder base:

```sh
export DATABASE_URL='<private disposable PostgreSQL administrator URL>'
export FAAS_PGTEST_TEMPLATE_DATABASE=1
export FAAS_TEST_KERNEL=/srv/fc/base/vmlinux-6.1.134
export FAAS_BUILDER_BASE_PATH=/srv/fc/base/runner-builder-amd64.ext4
export FAAS_GUEST_INIT='<static Linux amd64 guest/init binary built from this checkout>'
export FAAS_PUBLIC_IFACE='<host outward interface>'
export GREGALE_POSTGRES_NATIVE_HOST_CLASS=native
export GREGALE_POSTGRES_NATIVE_RESULTS_PATH=/private-test-directory/results.jsonl
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
