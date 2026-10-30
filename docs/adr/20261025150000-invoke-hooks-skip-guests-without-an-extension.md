# ADR-20261025150000 · Invoke hooks skip guests without an extension

- **Status:** Proposed. Amended 2026-10-27: the switch is on by default
  (see "Amendment 2026-10-27").
- **Date:** 2026-10-25
- **Amends:** the extension lifecycle contract in `pkg/extension` (wired for
  the invoke phase by #3003): one new guest acknowledgement value and a host
  backoff. No customer-facing change for apps without an extension.
- **Switch:** vmmd `invoke_hook_backoff` / `FAAS_INVOKE_HOOK_BACKOFF`,
  default on since 2026-10-27 (default off before). The guest-side ack ships
  unconditionally; with the switch off vmmd keeps sending every invoke event,
  as before.

## Context

Since #3003, vmmd sends an `invoke` lifecycle event to the guest for every
forwarded HTTP request that carries `X-Faas-Invocation-Id` or
`X-Faas-Invocation-Source`. The gateway stamps `X-Faas-Invocation-Id` on every
direct request, so every request sends one. Each event is asynchronous on the
host, but it is not free:

1. vmmd starts a goroutine, marshals JSON and dials the instance's
   Firecracker vsock socket;
2. Firecracker's vsock muxer opens a new vsock connection into the guest;
3. guest-init accepts it, decodes the event and dials
   `/run/guest/extension.sock`;
4. with no extension, that dial fails, guest-init still answers `OK`, and
   both sides close the connection.

Production-us, rc.294, 2026-10-25, app `h10-hello-node` (256 MB Node hello
world, Scale topology of 4 vCPUs, cgroup `cpu.max` of one core) on
gregale-compute-2. Numbers come from the VM cgroup's `cpu.stat`, per-thread
`guest_time`/`stime`, and KVM debugfs counters:

| Window | VM CPU | KVM exits |
| --- | --- | --- |
| 100 HTTP requests/s, 20 s | 5.7 ms per request | 113 per request |
| 100 invoke events/s alone (1 req/s keep-warm, idle window subtracted), 2,000 events | **4.4 ms per event** | **110 per event** |

The invoke event is therefore about three quarters of a hello-world request's
VM CPU. Most of its cost is guest vCPUs waking and halting (34 halt exits and
37 interrupt injections per event) as the vsock connection and guest-init's
goroutines move across four vCPUs. Under GCE nested virtualization each exit
costs roughly 3–4 times what it costs on bare metal. With one core per VM, the
event caps a single VM near 175 requests/s.

The contract calls lifecycle events best-effort hints.

## Decision

1. **Guest:** when guest-init's extension dispatch fails with
   `extension.ErrUnavailable` (no endpoint accepted the event), it answers
   the event with the new ack `16` (`VsockExtensionAckUnavailable`) instead
   of `0`. Every other outcome is unchanged: a delivered event and an
   extension that violated the contract still answer `0`.
2. **Host:** `JailerVMM.TriggerExtensionHook` reports ack `16` as an error
   wrapping `extension.ErrUnavailable`. vmmd's forward path keeps a
   per-instance backoff: after that answer it sends no invoke events to the
   instance for `extension.InvokeUnavailableBackoff` (30 s), then the next
   request sends one event as a probe. A transport failure or any other ack
   does not start a backoff.
3. The host backoff runs only while vmmd's `invoke_hook_backoff` switch is
   on. Turning it on for one compute node and comparing it with the others
   under the same load is the production check below.
4. The backoff is in-memory in vmmd, keyed by instance id. A restart forgets
   it, which costs one probe per instance. A frozen and thawed instance keeps
   its entry, because the same guest-init process answers. The map is swept
   of expired entries once it holds 4,096.

## Consequences

- An app without an extension pays one invoke event per instance per 30 s
  instead of one per request. On the measured app that removes ~4.4 of the
  5.7 ms of VM CPU per request, and vmmd's goroutine, JSON and vsock dial per
  request (about 12% of vmmd's CPU under that load).
- An extension that starts listening after the instance's first request
  misses invoke events for up to 30 s. Events were already best-effort and
  dropped whenever the endpoint was absent. Init, post-restore, pre-snapshot
  and shutdown events are not affected: guest-init sends those itself, not
  through this host path.
- **Version skew** (docs/release-compatibility.md): a new guest answering `16`
  to an old vmmd is read as "extension hook rejected (ack=16)" and logged at
  debug level, as for any failed best-effort hook. A new vmmd talking to an
  old guest-init always gets `0` and keeps sending events as today. The gain
  arrives as guests boot from a base image with the new guest-init and take
  fresh snapshots.

## Verification

- Unit: the guest answers `0`/`16`/`0` for delivered, unavailable and
  contract-violating dispatches; `TriggerExtensionHook` maps `16` to
  `extension.ErrUnavailable` and other non-zero acks to plain errors; the
  vmmd backoff holds events inside the window, releases one after it, keeps
  other instances unaffected, and sweeps expired entries.
- Production check after a release with both halves: turn the switch on for
  one compute node (it takes effect when vmmd next restarts), then repeat the
  100 req/s measurement on the same app with its instance on that node. The VM cgroup should fall from ~5.7 to about
  1.3 ms per request, KVM exits should drop by about 110 per request, and vmmd's
  CPU per request should drop by about 12%.

## Amendment 2026-10-27: on by default

The production check above ran on rc.297 on 2026-10-27, with the switch on
in compute-1's (fsn-2) `vmmd.toml`: k6 at 50 req/s for 120 s (6,000
requests) against `h10-hello-node`, CPU read from the cgroups' `cpu.stat`.
The VM cgroup used 19 s instead of 46 s (about 2.1 instead of 6.5 ms per
request) and vmmd 9.9 s instead of 12.4 s. The app's snapshot post-dated the
rc.297 guest-init that sends ack `16`; guests on older snapshots keep acking
as before and are not backed off, so they lose nothing.

vmmd's default is now on (`cmd/vmmd/config.go`). `invoke_hook_backoff =
false` in `vmmd.toml` or `FAAS_INVOKE_HOOK_BACKOFF=false` turns it off; on a
managed node, use a systemd drop-in, since the TOML is re-rendered.

Why the binary and not the node: release rollouts run `gregalectl manifest
render` on every compute node (`deploy/ansible/node_join.yml`), which rewrites
`/etc/faas/vmmd.toml` and `/etc/faas/schedd.toml` from the manifest. A
switch set by hand in those files is lost at the next rollout and is never
present on a new node; a default in the binary reaches both the same way.
