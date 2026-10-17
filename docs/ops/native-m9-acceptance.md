# Native x86 M9 migration recovery qualification

`make native-m9-acceptance` runs the first live migration recovery drill against
two designated, otherwise empty native x86_64 KVM compute hosts and their existing
PostgreSQL database. A skipped test or missing prerequisite fails qualification.
The gate never provisions hosts, migrates the database, changes node admission,
or seeds application/instance rows.

This slice exercises [incoming reservations](inbound-migration-reservations.md)
and [source retirement](migration-source-retirement.md):

1. Wake a pre-deployed request fixture through the production scheduler engine.
2. Migrate it through real vmmd RPCs and snapshot storage. After destination
   restoration, briefly SIGSTOP the source daemon to observe ownership commit
   while its original RAM/CPU charge remains retained.
3. Resume the source, let its real acknowledgement RPC complete physical cleanup
   and durable retirement, then discard the successful response in the test's
   gRPC client. This simulates reply loss at the client boundary; it does not
   inject a network partition or drop packets.
4. SIGKILL the original source vmmd, observe a new systemd invocation, replay the
   acknowledgement, and verify the destination's in-memory operation receipt.
5. Migrate the same instance back, verify a distinct wake/native generation,
   replay the original acknowledgement, and verify the replacement still serves
   the same operation exactly once.
6. Park the fixture; require zero resident VMs, retained RAM/CPU and pending
   source leases on both hosts; run the reviewed leak bundle on both peers.

The test calls the production scheduler migration adapter directly. It qualifies
the handoff and receipts. Automatic arbiter selection, gateway routing, full host
power loss, partitions and recreation remain separate drills. The
[automatic drain drill](#automatic-node-drain-and-controller-restart) below has
its own target and report.
Existing skipped `cmd/e2e` scenarios do not count as evidence for this gate.

## Prepare the designated pair

Use a dedicated acceptance fleet, isolated from customer work and other native
gates. The local runner must be root on x86_64 Linux. The runner and **both peers**
must have a root-owned regular `/etc/faas/m9-acceptance-host` marker that is not
writable by group or others. Do not designate ordinary production hosts.

Each peer needs Python 3.11+ with pidfd support, native Linux KVM, cgroups v2,
`systemd-detect-virt`, normal leak-check tools, active `faas-vmmd.service` and its
usual node-local schedd. Virtual machines, containers and nested virtualization
cannot qualify. Both daemons must already be registered and admitting in the same
database, use the same Firecracker version, advertise snapshot restore, and run
the **same reviewed vmmd artifact built from the clean checkout being qualified**.
Preserve their native generation journals and shared snapshot/rootfs storage.

SSH must work in batch mode with normal host-key verification. Its account must
be able to open the peer's vmmd Unix socket, forward Unix sockets, and run the
reviewed Python helper through `sudo -n`. RPCs travel inside authenticated SSH.
Run no other disruptive suite concurrently. The per-peer ownership lease excludes
other instances of this gate; operators must exclude unrelated suites.

Deploy [the Node 22 fixture](../../tests/native/m9/fixture/) using the ordinary
[deployment flow](../cli/deploy.md). Its manifest selects port 8080, `/healthz`,
`node server.mjs`, and a zero floor with at most one instance. Set application
environment variable `GREGALE_M9_FIXTURE_ID` to a fresh canonical UUID and record
the accepted app/deployment IDs. Use request execution mode, no sidecars, and an
idle timeout at least as long as `timeout_seconds` (for example 300 seconds for a
180-second drill). Keep the app's normal owner on the configured source. Let all
fixture instances park first, and send no external probe traffic during the run.

The fixture intentionally keeps its boot UUID and operation counter in guest
memory. Cold fallback loses this evidence. A migration run consumes two unique
operation IDs and a drain run consumes one; deploy a fresh fixture when its
bounded 128-operation receipt map fills.

## Run and retain evidence

Use a clean reviewed checkout, including no untracked files. Obtain the exact
vmmd binary installed on both peers: its hash must match the config and its Go
build metadata must contain this checkout's `vcs.revision` and
`vcs.modified=false`. Use the Go toolchain pinned by `go.mod`.

Create a private configuration **outside the checkout**. Replace all example
values with the designated fixture, inventory and reviewed artifact:

```json
{
  "version": 1,
  "app_id": "11111111-1111-4111-8111-111111111111",
  "deployment_id": "22222222-2222-4222-8222-222222222222",
  "fixture_id": "33333333-3333-4333-8333-333333333333",
  "timeout_seconds": 180,
  "source": {
    "name": "native-source",
    "ssh": "native-source",
    "socket": "/run/faas/vmmd.sock",
    "vmmd_sha256": "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
  },
  "destination": {
    "name": "native-destination",
    "ssh": "native-destination",
    "socket": "/run/faas/vmmd.sock",
    "vmmd_sha256": "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
  }
}
```

Supply `DATABASE_URL` through the runner's existing protected environment:

```sh
export FAAS_M9_CONFIG=/root/native-m9.json
export FAAS_M9_VMMD_BINARY=/root/reviewed-artifacts/vmmd
export FAAS_M9_CONFIRM=native-x86
make native-m9-acceptance
```

The gate prints a private `/tmp/gregale-native-m9-*/report.json` path, including
on preflight failure. Archive the entire directory before temporary files expire.
It contains the frozen config, raw Go JSON events, stderr, checkout/config/artifact
identities, every required phase proof, host boot identities, receipt bindings,
operation probes and independent restoration/leak results. Files are private
because host/fixture metadata may be sensitive. `DATABASE_URL` is not saved.

`native_qualified: true` requires the process, package, root test, all seven
subtests and their run/revision-bound proofs to pass. Required phases cannot skip.
Incomplete streams, duplicate/foreign proofs, receipt changes, lost source capacity
charges, cold fallback, failed restoration, leaks or failed lease release fail the
command. A passing report qualifies only this slice at its recorded revision.

## Failed or interrupted runs

Each peer creates an exclusive private
`/run/faas/native-m9-acceptance.owner.json` lease before faults. Signals use a pidfd
and the exact PID, start time, systemd invocation and binary hash. PID reuse cannot
redirect a signal to a replacement daemon.

On ordinary errors or catchable signals, the Go test and runner attempt bounded
restoration of the original paused invocation. Cleanup parks only a run-owned
instance/wake through the production scheduler. Uncertain handoffs retain the VM
and journals for inspection. There is no forced row, journal, snapshot, lease or
VM deletion fallback.

Only a complete passing run with independent post-run leak checks automatically
releases peer leases. Failed runs retain them, even if cleanup appears complete.
Runner/host power loss can leave a stopped vmmd and lease; there is no expiry-based
takeover. Inspect the private report, each peer lease, systemd invocation and
source/incoming receipts using the linked recovery guides. Resume only the
recorded original invocation after verifying its identity. Resolve the handoff,
park the run-owned fixture, and verify both peers' database/native capacity and
leak checks before an operator removes an inspected stale lease. Preserve source
receipts and native generation journals.

## Automatic node drain and controller restart

`make native-m9-drain-acceptance` uses the same pre-deployed fixture, native host
guards, artifact provenance, exclusive peer leases and private evidence format.
It requires an isolated fleet with exactly the designated pair admitting work,
and both node-local schedd services must already run the same reviewed binary
built from the checkout being qualified.

Add a `drain` object to a copy of the configuration above:

```json
"drain": {
  "api_url": "https://control.acceptance.example.test",
  "schedd_sha256": "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"
}
```

Use the actual HTTPS API origin and reviewed schedd digest. The URL cannot contain
credentials, a path, query or fragment. Supply `FAAS_M9_OPERATOR_TOKEN` through the
runner's protected environment: it must authenticate an allowlisted operator with
admin scope under the API's existing MFA policy. Authorization is checked against
the real node/progress routes before pausing a controller. Certificate verification
stays enabled, redirects are refused, and tokens are never put in configuration,
URLs or evidence.

```sh
export FAAS_M9_CONFIG=/root/native-m9-drain.json
export FAAS_M9_VMMD_BINARY=/root/reviewed-artifacts/vmmd
export FAAS_M9_SCHEDD_BINARY=/root/reviewed-artifacts/schedd
export FAAS_M9_CONFIRM=native-x86
make native-m9-drain-acceptance
```

The seven required phases perform these checks:

1. Verify API/database node identity, authorization, the isolated native pair and
   both reviewed daemons. Confirm the source is eligible for placement.
2. Wake the fixture through the scheduler engine and record its in-memory receipt.
3. Pause the destination schedd with a pidfd, then issue the real
   `POST /v1/compute-nodes/{name}/drain`. For multiple recovery sweep intervals,
   require the source to remain serving and draining, with retained RAM/CPU and
   no completion receipt. Run the production placement chooser against the live
   source row to verify that the draining generation refuses placement.
4. SIGKILL that paused destination schedd and observe a new reviewed systemd
   invocation. Restart intent is persisted before the signal, so restoration
   after a controller disconnect does not send SIGCONT to a dead PID.
5. Let the installed schedulers automatically migrate the original instance to
   the destination. The test does not invoke migration, recovery ticks or
   lifecycle completion itself. Verify its new wake, original physical source
   binding and identical in-memory operation receipt.
6. Require `maintenance` in the captured generation's successor, with zero source
   residents, retained RAM/CPU and source cleanup leases. Verify physical source
   retirement, the immutable completion receipt/audit outbox handoff, agreement
   with the real progress API, and receipt stability over later controller sweeps.
7. Park the fixture and run leak checks on both peers. Independently restore and
   verify both vmmd and schedd before the runner can release either peer lease.

The deterministic barrier is before migration: it demonstrates that an accepted
drain remains pending while its original resident work exists. The completion
phase checks actual source retirement and cleanup at observed completion; it does
not inject a post-commit acknowledgement fault into the running daemons. The
separate migration gate covers that client reply-loss boundary. This drill also
does not send concurrent customer requests or qualify an availability SLO.

A passing drain leaves the source in **maintenance** and the fixture parked on
the destination. Neither success nor failure automatically activates the source.
After inspection, use the normal operator activation workflow before another
suite needs that node to admit work. Failed runs retain their peer leases and any
uncertain workload evidence; follow the recovery procedure above. Each report
records `scenario: "drain"` or `"migration"`; the configuration and selected
target must match, and evidence from one scenario cannot qualify the other.

## Portable checks and heartbeat smoke

`make test-native-m9-contracts` tests both scenario verdicts, the authenticated
HTTPS drain client, peer ownership/PID fences, restoration after a stopped schedd
is killed, the HTTP fixture and one-shot reply loss. Both CI tiers run it and
compile the tagged package without native faults. These checks verify the
harness; native execution still requires the designated pair.

The previous heartbeat runner is available as `make native-m9-heartbeat-smoke`,
using its existing `FAAS_TWO_NODE_*` settings and `FAAS_M9_CONFIRM=native-x86`.
It stops peer B's vmmd and observes `node.failed`. Its success is heartbeat smoke
evidence only. Run it separately and restore/reconcile the pair before migration
qualification.
