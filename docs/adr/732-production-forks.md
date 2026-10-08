# ADR-732 · Production forks: quarantined debug restores of an app's capture

- **Status:** proposed
- **Date:** 2026-10-08
- **Amends:** spec §6.1/§6.2 (a new non-serving instance mode and how it
  counts against invariants 1 and 2), spec §7 (a per-instance egress mode),
  ADR-031 (a quarantined instance ignores the app egress allowlist), ADR-159
  (a capture can be restored into a non-serving instance).
- **Decision:**
  - **What a fork is.** A customer asks for a fork of an app. Gregale
    restores the app's newest non-stale capture of its live deployment
    (warm tier when the plan allows it, otherwise init, the same choice as
    `Engine.chooseWakeSnapshot`) into a new instance that never serves
    production traffic, cannot reach the network, and is destroyed when its
    TTL ends. Phase 1 never takes a new capture, so production is never
    paused by a fork. A `--live` fork (capture a running instance via
    `WarmSnapshot`) needs a non-servable snapshot tier and is out of scope.
  - **Intent table.** apid owns a new `app_forks` table: one row per fork
    request with `status ∈ {queued, restoring, running, expired, cancelled,
    failed}`, a lease (`lease_token/owner/expires_at`), `expires_at` (the
    TTL), and the source `deployment_id` and `snapshot_id` once resolved. A
    trigger enforces the transition graph and makes terminal rows
    immutable, mirroring `app_tasks` (ADR-230). schedd claims queued rows
    with `FOR UPDATE SKIP LOCKED` through a `ForkCoordinator` shaped like
    `AppTaskCoordinator`, and its sweeper destroys the instance and expires
    the row at `expires_at`.
  - **Instance mode.** A fork is an `instances` row with `kind='wake'` and a
    new `mode='fork'`. schedd stays the only writer.
    - **Invariant 1:** a fork does not count toward `max_concurrency`. The
      admission ledger gets a `KindFork` that `kindCountsConcurrency`
      excludes, like `KindAppTask` (not a serving replica).
    - **Invariant 2:** a fork counts toward the 47,600 MB RAM ceiling at
      `ram_mb + 8` like any live instance. Forks are admitted below tenant
      wakes: a fork is refused, never queued, when admission would need to
      park or evict a serving instance.
    - **Billing:** a fork bills like a running instance (plan RAM + 8 MB per
      second, spec §4.7). It is not a skippable mode like `mirror`.
  - **Never routed.** The gateway's live-target loader skips `mode='fork'`
    rows. Today it keeps any `running` row on a live deployment, so this
    filter ships in the same PR as the first code that can create a fork
    instance, never later.
  - **Quarantined network.** The fork keeps its NIC (Firecracker restores the
    snapshot's device set), inside its own netns as usual (invariant 5,
    ADR-009). `netns.Config` gets `Quarantine`, rendered as separate base
    chains (`quarantine_forward`, `quarantine_input`) at priority −10 in
    both the `ip` and `ip6` tables:
    - forward: accept `established,related`; drop everything else from
      `tap0`; drop any new flow to `tap0` that did not arrive on the
      platform veth (so a private-network side-link cannot reach the fork).
    - input: drop everything from `tap0`.
    - Separate base chains, not rules in the regular forward chain: a drop
      in any base chain is final, and several patch paths (private network,
      `insertNftRule`) insert accepts at the top of the regular chain, which
      would otherwise bypass a drop placed there.
    - `fcvm` also clears the allowlist, private network, static egress IP
      and operator bundle from a quarantined wake, so no route, SNAT
      registration or side-link exists for the chains to have to stop.
      The flag lives on `Instance.Net`, so in-place updates re-render it.
    Inbound DNAT from the veth peer keeps working, so the platform can still
    reach the guest port. `AppSpec.quarantine` (field 31) carries the flag
    on both the restore and the cold-boot wire.
  - **Secrets.** Secrets are in the guest's memory and in the captured
    drive1 (`upper/etc/faas/secrets.env`, ADR-210), so a fork cannot be
    redacted in memory. Gregale therefore treats creating a fork as reading
    secrets:
    - The API requires the secrets-read scope and MFA, like secret export.
    - Every create, access and delete writes an audit event.
    - The restore removes `secrets.env` from the fork's private drive1 copy
      before resume, so a later process start inside the fork cannot reload
      them; values already in process memory remain.
    - Quarantine makes credentials in memory unusable from the fork.
  - **Entitlement and limits.** Pro and Scale only. Default TTL 1 h, maximum
    4 h; at most one active fork per app and two per account. All of these
    live in `pkg/api/limits.go`.
  - **Access.** How a customer reaches a running fork (a fork-scoped
    authenticated route, and a guest exec channel over vsock) is a separate
    ADR. Until it lands a fork is only useful to operators, so the API stays
    behind `FAAS_APP_FORKS` and the capability stays `internal`.
- **Why:** reproducing a production bug today means redeploying and
  replaying traffic, which loses the in-memory state that caused it. A
  capture already holds that state (warm captures are taken from a running,
  framework-ready VM), and the restore path already gives each restored
  instance its own IP, netns, jail uid and reseeded RNG. What is missing is
  an instance that is guaranteed not to serve, not to reach the network,
  and not to outlive its purpose.
- **Consequences:**
  - A fork competes for tenant RAM. Two forks per account at Scale's 1 GiB
    is at most about 2 GB of the 47,600 MB budget, and forks lose to wakes.
  - A fork is a copy of production memory, including end-user data. It never
    leaves the node it was restored on, is destroyed at its TTL, and its
    private drive copy is deleted with the instance.
  - `instances.mode` gains `fork`. Triggers and readers that switch on mode
    (billing intervals, service capacity, environment-workload guards,
    reapers) must handle it; each is listed in the PR that adds the mode.
  - Apps with ephemeral-class secrets never publish a capture, so they
    cannot be forked. The API answers 409 with a clear code.
- **Rejected alternatives:**
  - **Networkless restore.** `SnapshotRef.networkless` removes the NIC, but
    Firecracker must restore the device set the snapshot was taken with, and
    vmmd rejects networkless restores of app captures.
  - **A routed instance with an auth gate.** One missing filter would send
    production traffic to a debug copy. Excluding the mode from routing is
    simpler to prove.
  - **A new snapshot tier for every fork.** Not needed until `--live`
    forks. Reusing the newest capture keeps GC, replication and invariant 3
    untouched.
  - **Redacting secrets from memory.** Not possible in general for an
    arbitrary process image.
- **Rollout (each step is one PR):**
  1. This ADR, the `app_forks` table and state layer, limits, and the
     flag-gated create/list/get/delete API that records intent.
  2. `netns.Config.Quarantine` and the `AppSpec` field that requests it,
     with metal tests that a quarantined guest cannot open any outbound
     flow and still answers on its port.
  3. a. Make a fork row safe before anything creates one: `mode='fork'`,
        `KindFork` (also when `SeedLedger` rebuilds after a restart),
        the scheduler `AppSpec.Quarantine`, the scheduler-side lease
        store, and a fork exclusion in every path that would route,
        park, snapshot, drain, reap or concurrency-count it (gateway
        live and smoke loaders, tcpd/udpd resolvers,
        `RunningInstanceForApp`, wake reuse, reaper, `ParkApp`,
        `StopInstance`, service drain). `snapshotAndPark` destroys a
        fork instead of capturing it, so no caller can turn fork memory
        into a restorable capture.
     b. The `ForkCoordinator` (claim, restore, renew, TTL teardown,
        abandoned-lease takeover) and its schedd wiring behind
        `FAAS_APP_FORKS`. `Engine.RestoreFork` does not use the wake path:
        it places, reserves `KindFork`, creates the `fork` row, restores
        the newest compatible capture of the pinned deployment with
        `Quarantine` and no sealed secrets, and publishes RUNNING. A fork
        whose scheduler stops is adopted by another if it is running, and
        failed with `scheduler_lost` if it was mid-restore.
  4. The drive1 secrets scrub on fork restores.
  5. Access (separate ADR), then promotion to `preview`.
- **Verification:**
  - PR 1: migration CHECK and transition-trigger tests; MemStore and
    PgStore parity; API tests for the flag gate, plan gate, scopes, limits,
    IDOR and audit.
  - PR 3: the invariant property tests gain fork instances
    (`TestProperty_EngineWake_RespectsMaxConcurrency`,
    `FuzzLedgerInvariants`), and a gateway test proves a running fork on a
    live deployment is never a target.
