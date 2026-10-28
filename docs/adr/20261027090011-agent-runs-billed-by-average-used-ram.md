# ADR-20261027090011 · Agent runs are billed by average used RAM

- **Status:** proposed. The used-RAM conversion factor is pending the financial-model update.
- **Date:** 2026-10-17
- **Amends (when accepted):**
  - Spec §4.7, for agent runs only. Its billing rule is "GB-RAM-hour computed on plan RAM size + 8 MB overhead, not sampled RSS".
  - The matching CLAUDE.md "load-bearing" bullet, which gains an agent-run exception.
- **Builds on:** ADR-20261027090010 (agent runs), ADR-1350 (USD pricing and plan entitlements), ADR-909 (measured admission), ADR-907 (free page reporting), ADR-238 and ADR-248 (tenant statements and rate cards), ADR-1162 (billing delivery finality and spend cap).
- **Related:** ADR-20261027090012 (time an agent run spends paused waiting on the model is not billed).
- **Decision:**
  - An agent run is billed for the memory it actually used, averaged over its duration, rather than for its configured size.
  - The measure is GB-seconds of the run's private memory. vmmd samples it on the host every second and reports it once at teardown.
  - A floor of 256 MB applies, plus the 8 MB per-VM overhead.
  - Agent GB-seconds draw on the plan's included GB-RAM-hours at a fixed conversion factor `k`. Beyond the allowance they bill as ordinary overage.
  - Apps, functions, jobs and every other workload keep configured-RAM billing.
- **Why:**
  - **Configured-RAM billing is unfair to agent runs.** Agents are spiky: a run can wait on the model at a few hundred MB, then run tests at 2.5 GB for one minute. Configured billing charges the headroom for the whole run.
  - **Sizing becomes harmless.** Average billing lets the platform boot agent VMs with generous ceilings, so sizing (ADR-20261027090010 §5) protects the run from OOM instead of driving the customer's cost.
  - **It matches the market.** Fly Sprites bill memory in use; AgentCore and Blaxel bill by consumption.
  - **Gregale's own cost already tracks use.** Measured admission and free page reporting do this today.
  - Owner decisions D6 and D11, 2026-10-17.
- **Consequences:**
  - **One fewer source of margin.** Billing configured RAM while admitting measured memory is where part of today's margin comes from: invariant 2 allows up to 2× the ceiling in configured reservations. Agent runs give that up, so `k > 1`.
  - Bills vary from run to run. Every run shows its memory curve and cost, and the default spend caps (ADR-20261027090010) apply.
  - A new billing-grade measurement in vmmd, and a new meter kind (`agent`) in meterd.
  - The financial model must be updated before acceptance; CLAUDE.md says it is never contradicted.
- **Rejected alternatives:**
  - **Configured RAM**: predictable, but customers pay for headroom.
  - **Peak per run, rounded to 512 MB**: simple, but one short spike bills the whole run at its height. The owner rejected it as unfair.
  - **Guest-reported usage**: the guest is customer-controlled and could under-report.
  - **Raw cgroup `memory.current`**: counts shared snapshot pages for whichever run touched them first (see Measurement).
  - **A separate agent price list**: two meters on one plan confuse customers, and the payment provider catalog has one metered price (spec §4.7).

## Measurement

**What is sampled.**
- Restored VMs map the snapshot's memory file through the Firecracker `File` backend. Unchanged pages are page cache shared between every VM restored from that snapshot, and the kernel charges a shared page to the cgroup that touched it first. Billing `memory.current` would make the first run after a restore pay for pages that later runs get free.
- The billed quantity is therefore the run's **private memory**: `anon` + `shmem` from the run cgroup's `memory.stat`.
  - Pages the guest allocated or modified are private (copy-on-write of snapshot pages, the guest's own page cache, the JavaScript heap once it runs).
  - Shared snapshot pages and host page cache for the read-only drives are not counted.
- vmmd already reads `memory.stat` (`pkg/fcvm/freeze.go`) and per-VM cgroup counters (`pkg/fcvm/cgroupstats`).

**How.**
- vmmd samples once per second for the life of the run VM.
- It sums `private_bytes × interval` into GB-seconds, counting the number of samples taken and expected.
- At teardown it reports `gb_seconds`, `samples`, `expected_samples`, `peak_private_bytes`, `paused_seconds` (ADR-20261027090012) and the method version, together with the run outcome.
- schedd writes these to `agent_runs`.

**Gaps.**
- Seconds without a sample (for example, across a vmmd restart) are billed at the run's own average.
- If more than 20% of expected samples are missing, the run is billed at its peak rounded up to 512 MB × duration, and the record says why.

**Cold-boot fallback.**
- A run that cold-booted because no usable snapshot existed (ADR-005) has all guest memory private from boot.
- So that the platform's fallback never costs the seller more, such a run is billed at its measured GB-seconds minus the preset's restored baseline × duration, never below the floor. The baseline is the p50 private memory one second after restore, across restored runs of the same preset and image digest.

**Floor.** The billed rate per second is max(private memory, 256 MB) + 8 MB.

**Excluded.** Seconds in which the VM is paused waiting on the model are excluded (ADR-20261027090012).

## Billing

**Units.**
- An agent run's billable quantity is `agent_gb_hours = billed_gb_seconds / 3600`.
- meterd rolls it into usage with `meter_kind = 'agent'` and converts it to plan units: `plan_gb_hours = k × agent_gb_hours`.

**Allowance and overage.**
- Converted agent usage draws on the same included GB-RAM-hours as everything else (200, 1,000 or 3,000 on Hobby, Pro, Scale; ADR-1350).
- Beyond the allowance it bills as the existing `gb_ram_hour` overage. The effective agent rate is therefore `k × $0.01` per used GB-h.

**`k`.**
- Set by the financial model. It recovers the overcommit margin that agent runs no longer contribute.
- The working range is 1.5 to 2, giving $0.015–0.02 per used GB-h. That stays inside competitors' memory rates ($0.009–0.024/GiB-h, read 17 October 2026), and Gregale includes CPU.
- `k` is a single constant in `pkg/api/limits.go` (`AgentUsedRAMConversion`), effective-dated through the plan history (ADR-1162).
- **Evidence (2026-10-19, not a decision).** The source is `scripts/experiments/agent-runs/economics/README.md` on `proto/agent-runs`: measured runs, ADR-20261027090015 admission, and the orderable OVH US agent node RISE-5 (EPYC 7413, 48 threads, $199/mo).
  - **Break-even is k ≈ 1.9** at 60% utilization and 10 s model calls, for a mix of light triage runs, long sessions and tool-heavy runs. So the working range of 1.5–2 sits at break-even when usage is billed at the overage rate.
  - Agent nodes run out of CPU before RAM. Harness start-up and tool processes cost CPU, while a run is billed only about 264 MB (the floor) for each unpaused second.
  - **k ≈ 4 gives about 53% node margin.** A 4-node launch cell makes about 45% after fixed costs, or 68–75% when usage comes from plan allowances. The price is still 20–30× below E2B per run.
  - The financial model should confirm k. The largest uncertainty is CPU per run on real sessions; measure it in the ADR-20261027090015 §6 native check.

**Statements.**
- Invoices gain a line "Agent runs (memory used)" showing `agent_gb_hours`, `k` and the converted GB-h.
- Tenant statements (ADR-238) gain `agent_gb_hours` per tenant, so sellers can price their customers with tenant rate cards (ADR-248). Gregale does not bill tenants.

**Unchanged.**
- Overage caps and spend caps (spec §4.7.1, ADR-1162) apply to converted agent usage like any other usage.
- Free has no agent runs.

## Worked example

This example is illustrative and uses `k = 2`. A 5-minute run spends 4 minutes at 400 MB private and 1 minute at 2.5 GB, on a 3 GB ceiling.

| Meter | Billed | GB-h | Plan GB-h | Overage cost |
|---|---|---:|---:|---:|
| Configured RAM (today's rule) | 3 GB × 5 min | 0.250 | 0.250 | $0.0025 |
| **Average used** | 0.82 GB × 5 min | 0.068 | 0.137 | $0.0014 |

## Invariants and checks

- **Invariant 2 is unchanged.** Admission still uses measured memory and the reservation ceiling. Billing does not affect admission.
- **Property tests:**
  - billed GB-seconds ≥ the floor × duration;
  - billed GB-seconds ≤ the ceiling × duration;
  - paused seconds are never billed;
  - the gap rule and the cold-boot rule never bill more than the peak step would.
- **Metal tests:**
  - private-memory sampling across a restore;
  - two concurrent restores from one snapshot bill within a stated tolerance of each other;
  - a vmmd restart mid-run applies the gap rule.

## Metrics (§12 additions)

- `agent_run_billed_gb_seconds_total`
- `agent_run_sample_gap_ratio`
- `agent_run_billing_fallback_total{reason}`, with reasons `gaps` and `cold_boot`
- `meterd_agent_plan_gb_hours_total`

## Rollout

1. The financial-model update sets `k` and checks the plan economics with agent-shaped usage. The data comes from the ADR-20261027090010 prototype, which reports average, peak and configured GB-h for each run.
2. vmmd measurement, recorded on runs but not billed. Compare it against configured billing on dogfood traffic (ADR-20261027090010 milestone A).
3. meterd's `agent` meter kind, conversion, statement lines and tenant statements, still behind the `agent_runs` switch.
4. Accept this ADR, and amend spec §4.7 and the CLAUDE.md bullet in the same change.
5. Turn billing on with the seller beta (ADR-20261027090010 milestone B).

## Open questions

1. The value of `k`, and whether it differs by plan.
2. Whether the floor should be 256 MB or follow the preset's measured baseline.
