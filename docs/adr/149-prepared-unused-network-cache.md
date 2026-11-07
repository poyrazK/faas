# ADR-149 · Prepare unused network namespaces before a wake

- **Status:** accepted; on by default since 2026-10-12; spare lifetime amended 2026-10-17; native recovery amendment proposed 2026-11-07 (amendments below)
- **Date:** 2026-09-05
- **Amends:** spec §6.2 resource allocation timing and §7 network setup timing.
- **Decision:** vmmd may hold a bounded cache of fresh network namespaces which
  have never hosted a VM. The default was disabled until 2026-10-12 and is
  now `DefaultPreparedNetworkCacheSize` (4). `prepared_networks` in
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
  The documented default guest-port representations (`0` and `8080`) are
  equivalent for this comparison: both install the same `:8080` DNAT rule.
  All other identity and policy fields still require exact equality; invalid
  ports are not normalized into eligibility.
  *Amended 2026-09-24:* the guest port is not part of the cached policy. A
  custom port (1–65535) is eligible. When a claimed entry differs from the
  request only in its guest port, vmmd replaces the entry's `prerouting`
  chain, the only rules the port renders, with the request's DNAT rules in
  one `nft -f` transaction before any VMM starts. If that fails, vmmd
  rebuilds through ordinary setup. Before this, every app on a non-default
  port (Node's 3000, Python's 8000) rebuilt its namespace on every wake:
  prod measured 40–114 ms of `setup_network` on 24 of 24 such wakes, while
  8080 apps on the same node hit the cache every time.
- **Lifecycle:** Replenishment follows successful wakes and runs in one daemon
  worker with bounded operations. Entries expire after 60 seconds. While a
  configured cache remains enabled, the most recently observed eligible policy
  stays prepared; these host network objects have no VMM, guest memory, CPU or
  tenant cgroup. Maintenance runs every 15 seconds and
  replaces unused entries aged 30 seconds before they become unclaimable.
  This prevents an old spare in a partially consumed pool from expiring
  between maintenance ticks. Refresh uses the existing capacity and teardown
  rules. A policy change evicts obsolete unused entries.
  Shutdown drains unused networks after RPC shutdown. Failed teardown keeps
  the slot reserved and retries, rather than exposing a surviving network to
  another VM. Startup removes only UUID-qualified `fc-prepared-*` names left
  by an interrupted daemon; claimed instance names use ordinary VM recovery.
  *Amended 2026-10-17 (H10-141):* entries now stay claimable for one hour
  (`PreparedNetworkCacheTTLSeconds`) and are replaced at 30 minutes;
  maintenance still runs every 15 seconds (`PreparedNetworkMaintenanceSeconds`)
  so failed teardowns are retried as before. At 60 seconds an idle node
  rebuilt every spare each 45 seconds. Each rebuild writes about fifteen
  kernel bridge-port and networkd carrier lines, about 4,800 journal entries
  an hour with the default four spares. That was 96% of an idle production
  compute node's journal: the 512 MiB journal held four days instead of
  `MaxRetentionSec=7day`, and `FaasHostJournalNearLimit` fired continuously.
  Age was never a correctness guard: a claim re-checks namespace identity,
  veth ownership and the complete network config, and any mismatch uses
  ordinary setup.
  [ADR-460](460-prepared-network-policy-retention.md) proposes preserving fresh
  spares for other exact policies within the same global capacity, replacing
  only one oldest spare when a new target policy is absent from a full pool.
- **Bridge identity:** Enabling the cache pins the bridge's current MAC address
  without changing its value. Automatic MAC selection from newly attached
  veth ports can invalidate running namespaces' cached gateway MACs. A first
  private hardware run exposed HTTP timeouts during replenishment on an
  automatically addressed bridge; the platform SSD bridge was already pinned.
- **Default on (amended 2026-10-12):** The Ansible `vmmd_service` role has
  set `FAAS_PREPARED_NETWORKS=4` since rollout, but compute nodes joined
  through `node_join` never received that drop-in. Their vmmd ran with the
  code default of 0, and prod restores paid `setup_network` p50 66 ms on
  every wake. The code default is now `DefaultPreparedNetworkCacheSize` (4),
  so the cache no longer depends on how the node was provisioned.
  `prepared_networks = 0` or `FAAS_PREPARED_NETWORKS=0` still disables it.
  Under `native_process_recovery`, which refuses the cache until claims are
  journaled ([ADR-568](568-environment-gitops-contract.md)), the default is 0;
  an explicit size is kept so vmmd still reports the conflict. On a host
  without the tenant bridge (`br-tenants`), a default-sized cache is skipped
  with a warning, and wakes set up their networks inline; an explicitly
  configured cache still fails vmmd startup there. Production hosts get the
  bridge from `br-tenants-up.service`, which `faas-vmmd.service` orders
  before vmmd.
  Evidence, Firecracker 1.17 with guest kernel 6.18, 1 GiB guest, nested
  KVM test node, 20 restores each: restore wall time p50/p90 fell from
  192/208 ms to 130/172 ms. Network setup ran concurrently with drive
  staging and competed with it for CPU, so `stage_drives` also fell from
  80 to 40 ms p50.
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
  delayed backend fallbacks and requires platform-only snapshot wake p95 below
  350 ms on the SSD node. Proxy first byte, Cloudflare, client network, and
  physical distance are excluded.

## Amendment 2026-11-07 (proposed): prepared networks under native recovery

- **Status:** proposed, for review before any code or node run. The
  decision of record stays the one above until this amendment is accepted.
- **Why now:** strict budgets activate only on nodes whose vmmd runs
  `native_process_recovery` (ADR-1750,
  [ADR 20261107160000](20261107160000-budgets-ga-and-strict-billing-subject.md)),
  and that mode refuses this cache (ADR-568: "claiming a cache entry can
  rename its namespace before the full lease is journaled"). In native mode
  each network command batch runs as its own journaled host helper: a forked
  helper in a private cgroup, with a durable launch record and a durable
  retirement. Building a namespace takes several such runs. The n2 lane runs
  of 2026-10-24 measured `setup_network` at about 0.5 s per native wake,
  against 66 ms p50 for an uncached non-native wake and none for a cache
  hit. Strict egress also skips the cache in every mode (`acquireWakeNetwork`),
  because a warm spare has already sent bytes no budget funded.

### Decision

1. **A spare is a native launch record that never launches.** The pool
   prepares each spare under its own record, for the pseudo-instance
   `prepared-<uuid>` (namespace `fc-prepared-<uuid>`, so the journal's
   `Netns == "fc-" + Instance` rule holds unchanged). The record is
   unauthorized and has no PID, the same state an ordinary launch passes
   through between `prepare` and `launch`. The pool worker registers the
   spare's generation as a local producer, and every setup command runs as a
   helper scoped to that record (`nativeHostHelperEffect`). The allocator
   holds the spare's slot through recovery like any journaled lease. The
   helper producers are therefore fenced by the same journal, file lock and
   kernel boot identity as a VM launch.
2. **A claim is journaled before the namespace moves.** Claiming spare `S`
   for instance `T`:
   1. Under `S`'s lock, check that `S` is the current local generation, still
      unauthorized and unclaimed, and that its namespace and veth still
      exist (an in-process probe, no helper). Then
      durably write `S` with `claimed_by = T`. From then on no helper or
      second claim can act for `S`.
   2. Under `T`'s lock, prepare `T`'s record through the ordinary
      `prepareGeneration` path. Its lease is the allocator-adopted lease
      (same slot, UID, IP and veths, namespace `fc-T`), and it carries
      `prepared_from = {S, generation}`.
   3. Still holding `T`'s lock, move the namespace `fc-S` to `fc-T`
      (`movePreparedNetns`). This is an in-process producer under the
      launch lock, as `removeScopedStaleNetnsMarker` already is.
   4. Mark `S` `resources_removed`: its objects now belong to `T`. Then
      rewrite `T` as an ordinary version-3 record without `prepared_from`.

   A port retarget (`preparedNetworkRetargetCommands`) runs afterwards as one
   `nft -f` helper under `T`. A default-port app runs no helper at all on a hit.
3. **Recovery classifies each crash point.**
   - An unclaimed `S` is retired like any unlaunched record, which removes
     `fc-S` and its veths. This replaces today's startup sweep of
     `fc-prepared-*` names.
   - `S` claimed by `T` with no `T` record (crash after step 1): retire `S`.
   - `S` claimed by `T`, and `T` naming `S` with `S` not yet marked removed
     (crash after step 2 or 3): retire `T`. Its network cleanup may remove
     either `fc-S` or `fc-T`, because one slot holds one namespace and one
     veth pair. Then mark `S` removed.
   - A launched `T` follows ordinary live-VM recovery; recovery finishes
     step 4 if it was interrupted.

   The network inventory accepts `fc-prepared-*` names only for held spare
   records. Slot quarantine accepts one slot held by a linked `S`/`T` pair
   and nothing else.
4. **Strict egress gets closed-gate spares (phase 2).** A spare for an
   account with a strict egress cap is built with the ADR-1748 budget gate
   installed before its peer is raised, with zero quota. The segmentation
   IFB is built while the TAP is down, as `SetupCommands` already orders it.
   The gate drops everything the namespace sends, so nothing reaches the
   billable root veth before funding. Under the attempt lock, the claim
   applies the attempt's first funded quota and its deadline in one
   `nft -f` helper. That is the same flush-and-add the refill path uses,
   applied once for the first sequence. Strict spares form their own pool
   partition (policy field `strictEgress`). The non-native path keeps
   skipping the pool for strict egress until phase 2 is accepted for both
   modes.
5. **Rollout and N-1.** The new fields are `claimed_by` and
   `prepared_from`, written as version-4 records. vmmd writes them only when
   a new setting, `native_prepared_networks`, is on (default off), following
   the `native_launch_owner_records` pattern. Release N ships the reader and
   the gated writer. Turn the setting on only once every compute node and
   rollback target can read version 4. With it on, `prepared_networks`
   defaults to `DefaultPreparedNetworkCacheSize` under native recovery
   instead of 0. A graceful stop drains spares and finishes claims, so it
   leaves no version-4 record and allows a rollback. A crash can leave
   version-4 records that only release N or later recovers.

### Validation

- **Unit:** claim races; injected write failures at each claim step (the
  journal's `writeRecord` hook), restarting recovery after each; inventory
  and quarantine for linked pairs; refusal of helper commands for a claimed
  spare; TTL expiry and policy eviction under native recovery.
- **Metal (n2, native recovery):**
  - the prepared-network metal suites run in native mode;
  - lane B vmmd restarts with spares ready and with a claim stopped at each
    step;
  - `leakcheck` after each run;
  - restore wakes with wake-phase timings. The gate is `setup_network` p95 on
    a cache hit, compared against a non-native cache hit on the same node.
    The numbers are to be set from that run, not assumed here.
- **Phase 2:** the ADR-1748 stream and packet metal tests on a claimed strict
  spare. Root veth RX may not exceed the funded bound, and bytes before
  claim must be zero.

### What this does not fix

The same n2 runs measured about 0.6 s of other native machinery on each
wake: the release helper, `bind_tun`, the jailer gate, pre-boot files and
snapshot staging. With the network cached, a native wake still pays that on
top of today's restore, well past the 350 ms platform restore target. Strict budgets need that
cost removed as well before production runs native recovery. That is a
separate ADR-568 amendment, proposed after this one is reviewed.
