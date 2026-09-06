# ADR-149 · Prepare unused network namespaces before a wake

- **Status:** accepted, opt-in rollout
- **Date:** 2026-09-05
- **Amends:** spec §6.2 resource allocation timing and §7 network setup timing.
- **Decision:** vmmd may hold a bounded cache of fresh network namespaces which
  have never hosted a VM. The default is disabled. `prepared_networks` in
  vmmd.toml, overridden by `FAAS_PREPARED_NETWORKS`, selects 0–16 entries.
  Each entry reserves a unique allocator slot, UID, IP, veth pair and TAP and
  contains the complete default network policy and requested egress cap.
  Reservations are counted separately from admitted VMs: they have no guest
  memory, VMM process or tenant cgroup. All limits live in `pkg/api/limits.go`.
- **Ownership:** Claim atomically transfers the reserved slot to the requested
  instance and binds the namespace to `fc-<instance>`. The former cache name
  is removed before VMM startup. An entry can be claimed only once. Normal
  VM teardown destroys it; a namespace used by a tenant never returns to the
  cache. Duplicate instance IDs cannot replace an existing lease.
- **Policy:** Initially, builders, static egress IPs, per-app allowlists and
  operator bundles use ordinary setup. Eligible entries must match the rate,
  conntrack cap and address base, and the complete resulting network config
  is compared again after request validation. A mismatch rebuilds the network
  through the ordinary setup path before starting any VMM.
- **Lifecycle:** Replenishment follows successful wakes and runs in one daemon
  worker with bounded operations. Entries expire after 60 seconds. While a
  policy remains recently observed, maintenance runs every 15 seconds and
  replaces unused entries aged 30 seconds before they become unclaimable.
  This prevents an old spare in a partially consumed pool from expiring
  between maintenance ticks. Refresh uses the existing capacity and teardown
  rules; an idle cache stops replenishing. A policy change evicts obsolete unused entries.
  Shutdown drains unused networks after RPC shutdown. Failed teardown keeps
  the slot reserved and retries, rather than exposing a surviving network to
  another VM. Startup removes only UUID-qualified `fc-prepared-*` names left
  by an interrupted daemon; claimed instance names use ordinary VM recovery.
- **Bridge identity:** Enabling the cache pins the bridge's current MAC address
  without changing its value. Automatic MAC selection from newly attached
  veth ports can invalidate running namespaces' cached gateway MACs. A first
  private hardware run exposed HTTP timeouts during replenishment on an
  automatically addressed bridge; the platform SSD bridge was already pinned.
- **Fallback:** A cache miss, an unsupported policy, an expired entry or a
  failed namespace transfer uses the existing setup path. A failed transfer
  must remove its network before its slot is available again. Cache size does
  not increase admitted VM count, RAM, CPU quota or per-app concurrency.
- **Why:** The SSD prototype removed about 61 ms of combined restore and
  first-response p95 in one paired comparison. Preparing host network objects
  outside the request can recover this time without retaining parked guests.
  The prototype alone does not establish the full-wake SLO; replenishment
  contention and real gateway traffic require measurement.
- **Validation:** Race tests cover unique concurrent claims, duplicate instance
  IDs, expiry, policy changes, failed transfer, retained failed teardown and
  daemon shutdown. Linux tests verify namespace identity, existing destination
  protection, current policy, startup recovery and resource removal. Real
  restore tests must retain entropy/clock/UUID checks, quotas, cold-boot
  fallback and leak checks. Full gateway acceptance counts failures and
  delayed backend fallbacks and requires p95 below 350 ms on the SSD node.
