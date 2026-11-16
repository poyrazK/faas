# ADR-20261112120603 · Sustained CPU abuse detection

- **Status:** accepted
- **Date:** 2026-11-12
- **Relates to:** ADR-361 (tenant egress default-deny, account abuse hold), ADR-368 (build-time abuse signature scan), ADR-373 (guest DNS policy), ADR-1155 (automatic CPU captures on saturation)
- **Context:** Free compute is mined. ADR-361 closes the mining-pool ports, ADR-373 refuses known pool names and ADR-368 blocks unmodified miners at deploy. None of them sees a miner that is packed, fetched at runtime, or that talks to a pool or a pool proxy over 443 through a name that is not on the blocklist. What every miner shares is its CPU profile: it pins its CPU quota and keeps it pinned. A Free app is request-only (no workers, services, jobs or crons) and freezes 15 s after its last request, so a Free miner must also keep feeding itself requests to stay awake. Legitimate Free apps do not hold their whole CPU quota for many minutes; legitimate paid apps (batch, encoding, inference) do.
- **Decision:**
  1. **Signal.** schedd's instance stats poller tracks one episode per instance: its host CPU use is at least `api.CPUAbuseSaturationPercent` (90) of its sustained cgroup quota from the admission ledger (the same quota ADR-1155 captures read). A dip shorter than `api.CPUAbuseGrace` (30 s) keeps the episode; a longer dip, an unknown quota (including a startup boost), or the instance leaving the snapshot (frozen, parked, destroyed) ends it. An episode lasting `api.CPUAbuseSustainedAfter` (15 minutes) raises one signal; the next signal needs a new episode. The episode tracker is shared with ADR-1155 and runs on every sample, independent of `FAAS_PROFILING_ON_DEMAND`.
  2. **Action by plan.** The engine resolves the app's resource owner and plan:
     - **Free, enforce mode:** the instance is recycled exactly like an ADR-361 egress recycle: warm snapshots are marked stale (they were captured after traffic and may carry the miner; the init snapshot is kept), the VM is destroyed, and the instance transitions to STOPPED with kind `cpu_sustained`. The recycle counts toward the ADR-361 escalation (`EgressFanoutHoldRecycles` recycles of any reason within `EgressFanoutHoldWindowSeconds`), and a hold it tips records reason `cpu_sustained`.
     - **Free, shadow mode:** nothing is done to the instance.
     - **Hobby, Pro, Scale:** flagged only, in either mode. Paid plans run CPU-bound work legitimately and carry a card on file; an operator reviews them.
  3. **Signals.** Every signal is audited as `instance.cpu_abuse` (subject: the app; data: instance, account, plan, action, mode, used and quota millicores, sustained seconds) and counted in `schedd_cpu_abuse_signals_total{plan,action}` (`recycled`, `shadow`, `flagged`). `FaasTenantCPUAbuse` warns on any increase; `FaasAccountAbuseHold` already covers holds.
  4. **Hold reason.** `accounts.abuse_hold_reason` accepts `cpu_sustained`. The hold is the ADR-361 hold in every other respect: account-scoped, separate from billing, enforced by every suspended-account gate, released only by an operator.
  5. **Rollout switch.** `cpu_abuse_mode` in schedd.toml, overridden by `FAAS_CPU_ABUSE_MODE` (an environment drop-in survives the manifest render): `off`, `shadow` (default), `enforce`. Unlike ADR-361 decision 8 this control ships behind a switch, because its false positives are legitimate customers rather than packets: the operator reviews a week of production `shadow` signals, then sets `enforce`. Any other value fails schedd startup.
- **Consequences:**
  - A Free miner gets at most 15 minutes per instance before the recycle and two recycles before the account is held, instead of running until someone notices.
  - A miner that throttles itself below 90% of its quota is not detected. It then gets less than the Free CPU quota the plan already gives away, which is the cost the plan accepts.
  - A legitimate Free app that holds its CPU quota for 15 minutes under real traffic is recycled; a repeat within the hour holds the account until an operator releases it. Shadow mode is how that rate is measured before enforcing.
  - The escalation counter was per schedd and in memory (ADR-361's known limit); ADR-20261113133500 stores it, so restarts and other schedds no longer reset it.
- **Rejected alternatives:**
  - Recycling paid plans too: CPU-bound paid workloads are legitimate and the plan, not a heuristic, is their limit.
  - Requiring low request traffic as a second condition: a Free miner already has to send itself requests to stay awake, so the condition adds evasion room without removing false positives.
  - A daily CPU-seconds budget for Free: a separate pricing decision; this ADR only removes sustained abuse.
  - No switch (ADR-361 decision 8): the false-positive rate on real Free traffic is unknown until measured.
