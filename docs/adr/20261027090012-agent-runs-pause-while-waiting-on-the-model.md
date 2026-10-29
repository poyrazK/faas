# ADR-20261027090012 · Agent runs pause while they wait on the model, unbilled

- **Status:** proposed
- **Date:** 2026-10-17
- **Amended by (proposed):** ADR-20261027090015 §2. A run that stays paused can be compressed into memory-only zswap under memory pressure, so its memory no longer stays resident.
- **Amends (when accepted):** ADR-1151, for agent runs only. ADR-1151 keeps jobs and app tasks out of quiet pause and gives quiet-paused time no billing exception.
- **Builds on:** ADR-1151 (pause tiers), ADR-906 (pause measurements), ADR-989 (outbound relay), ADR-909 (measured admission).
- **Related:** ADR-20261027090010 (agent runs), ADR-20261027090011 (average used RAM billing).
- **Decision:**
  - vmmd quiet-pauses an agent run VM while the harness does nothing but wait on a call through the outbound relay: no relay bytes, no other active network flow, idle guest CPU.
  - It resumes the VM when the relay delivers bytes, or when anything else needs the guest.
  - The paused seconds are excluded from the run's billed GB-seconds.
  - The run stays `running`. Nothing is written to the database and schedd is not involved.
- **Why:**
  - Owner decision D7 (2026-10-17): time an agent spends waiting on a long model call is not billed.
  - Model calls are a large share of an agent run's wall time.
  - A paused VM uses no host CPU, which frees active vCPU, the binding admission limit in ADR-1151's simulation.
  - ADR-1151's own machinery does the pausing (0.4 ms pause and resume; +1–3 ms on the next byte, per ADR-906). Only the activity signal and the billing rule are new.
- **Consequences:**
  - Agent runs pay only for the seconds their harness is doing something.
  - Nodes hold more concurrent agent runs, because paused runs do not count toward active vCPU.
  - The relay becomes an activity source and a resume trigger in vmmd.
  - The guest's in-process timers stop while it is paused, as for functions under ADR-1151.
  - Revenue per agent run falls by the paused share. The financial model accounts for it through `k` (ADR-20261027090011).
- **Rejected alternatives:**
  - **ADR-1151's schedd-owned FROZEN state for each model call:**
    - a database write and a schedd round trip per call;
    - thaw re-admits the full reservation, and destroys the VM when the reservation does not fit, which would kill a run mid-call;
    - its TTL and parking semantics have no meaning for a one-shot run.
  - **Snapshotting the VM to disk while it waits:**
    - writes the VM's RAM on every park, since there is no diff-snapshot support in `pkg/fcvm`;
    - needs a single-use snapshot kind and resumable relay connections.
  - **Pausing on idle CPU alone:** stalls harnesses that wait on direct network flows or timers.
  - **Extending the same rule to apps and functions:** it changes ADR-1151's function semantics, and is out of scope.

## Decision

### 1. Eligibility

schedd marks a run eligible in the run spec (`model_wait_pause_after_ms`, 0 = off) when all of these hold:
- the run's kind is `KindAgentRun`;
- its agent version does not opt out (`pause_during_model_calls: false`, for harnesses with heartbeat-sensitive connections);
- the run has no private-network attachment;
- the run has no attach or exec session.

vmmd's `agent_wait_pause` switch defaults off.

### 2. When vmmd pauses

An eligible run VM is quiet-paused when, for `QuietPauseAfterMillis` (2 s, the existing constant):

1. At least one relay connection is open, and no bytes have crossed any relay connection in either direction.
2. The instance's netns has no tenant-egress flow other than the relay with traffic in the window. vmmd checks conntrack counters. A harness waiting on a direct HTTPS fetch or a git pull is not paused.
3. The run cgroup's CPU usage grew less than `AgentWaitPauseCPUMaxMillis` (50 ms) over the window, according to vmmd's `cpustats` cache. A harness running tests while it waits on the model is not paused.
4. No output-stream frame is in flight to vmmd.

The pause uses ADR-1151's quiet-pause controller and ordering (`pkg/fcvm/quiet_pause.go`): the paused state is set before the last activity check and before the Firecracker call.

### 3. When vmmd resumes

- **Relay bytes.** The relay splice in vmmd reads from outboundd. When bytes arrive for a paused instance, it calls `QuietResume` before writing them to the guest's vsock. This is the first new resume trigger, alongside ADR-1151's forwarded requests.
- **Any vmmd operation that needs the guest** resumes it first, exactly as in ADR-1151: teardown, timeout kill, cancellation, output-stream reads and snapshot.
- **The run's deadline.** The host timer keeps running while the VM is paused. A run that reaches its timeout while paused is resumed and killed as usual.

The guest's clock is not stepped on resume. ADR-906 measured a constant offset of about 75 ms. A harness read timeout that would have expired during a long call still expires; pausing does not lengthen any call.

### 4. Billing

- vmmd excludes from the run's GB-second integral (ADR-20261027090011) every second in which the VM was quiet-paused under this ADR, and reports them as `paused_seconds`.
- The 2-second quiet window before each pause is billed.
- Memory stays resident and booked: measured admission still counts it, and invariant 2 is unchanged. Only billing changes.

### 5. Admission

- Quiet-paused agent run VMs count no active vCPU, per ADR-1151 §3. Their booked vCPU stays under `QuietPauseMaxVCPUOvercommitPercent` (300%).
- A resume never waits for admission. Contention is soft and is watched through CPU pressure (PSI), as in ADR-1151.

## Limits (`pkg/api/limits.go`)

| Name | Value |
|---|---|
| `QuietPauseAfterMillis` | 2,000 (existing) |
| `AgentWaitPauseCPUMaxMillis` | 50 |

## Metrics (§12 additions)

- `vmmd_agent_wait_pause_total{outcome}`
- `vmmd_agent_wait_resume_seconds`
- `vmmd_agent_wait_paused_instances`
- `agent_run_paused_seconds_total`

The per-run timeline (ADR-20261027090010) shows paused intervals.

## Rollout

Each step is one PR. VM-lifecycle PRs run `make test-metal` and `make leakcheck`.

1. This ADR, accepted together with ADR-20261027090011.
2. The vmmd relay activity signal and the resume trigger in the relay splice, behind `agent_wait_pause`. Metal tests:
   - a slow relay response pauses the VM, and the first byte resumes it;
   - an active direct egress flow prevents the pause;
   - CPU load prevents the pause;
   - teardown and a timeout while paused work;
   - 1,000 pause and resume cycles leave no leaks.
3. schedd eligibility in the agent run spec.
4. `paused_seconds` excluded from billed GB-seconds, with a property test that paused seconds are never billed.
5. Turn it on with ADR-20261027090011 billing at milestone B of ADR-20261027090010. Before then, the dogfood canary compares resume latency and errors with the pause on and off.

## Open questions

1. Whether a minimum pause length should apply before a pause becomes unbilled. Today every paused second is unbilled; the 2-second quiet window is always billed.
2. Whether streaming model responses with long gaps between tokens should pause between bursts, or only before the first token.
