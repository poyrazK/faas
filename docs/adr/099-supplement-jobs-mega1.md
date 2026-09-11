# ADR-099 · Jobs (run-to-completion workloads) — Mega-1 supplement

- **Status:** accepted (supplement to ADR-099 v1, status: proposed)
- **Date:** 2026-08-29
- **Branch:** `jobs/mega1`
- **Slot bank:** 00571–00578 (migrations), 0 fences consumed.

This supplement records the as-built deviations from ADR-099 v1
(`docs/adr/099-jobs.md`) introduced by Mega-1 (issue #1184
Workstream A). ADR-099 itself is unchanged; reading both in
order is the canonical reference.

## Locked deviations

1. **Slot bank 00517–00524 is fenced by ADR-134 PR-A.** Mega-1
   migrations start at 00571 per the plan (no renumber dance
   needed — sibling PRs claim reservations 00528-00532 plus
   00535 (PR #1191 mail_suppressions) and 00543 (PR #1204
   upload_sessions), all of which fall outside the Mega-1 range)
   (`/Users/poyrazk/.claude/plans/logical-beaming-church.md`).
   A `git log -- migrations/ | grep reserve_slot` sweep is the
   pre-merge gate; any fence below 00571 missing in this PR is
   a blocker.

2. **Plan caps as-built** (table 1 in §3 of ADR-099 is replaced
   by this table — the ADR text said "to be defined"):

   | Plan  | PerAccount | Concurrent | RAM MB | TaskTimeout s | Parallelism | Tasks/Run |
   |-------|------------|------------|--------|---------------|-------------|-----------|
   | Free  | 0          | 0          | 0      | 0             | 0           | 0         |
   | Hobby | 5          | 3          | 512    | 300           | 10          | 100       |
   | Pro   | 25         | 8          | 2048   | 1800          | 25          | 1000      |
   | Scale | 100        | 32         | 4096   | 3600          | 50          | 5000      |

   Source: PR #916 as-built table, encoded in `pkg/api/limits.go`
   as the `JobMaxPerAccount` / `JobConcurrentPerAccount` /
   `JobRAMMB` / `JobTaskTimeoutSec` / `JobMaxParallelismPerRun` /
   `JobMaxTasksPerRun` arrays (indexed by plan).

3. **Job RAM vs admission ceiling.** §4.6 says job VMs are
   tenant work. ADR-099 v1 was silent on the per-node admission
   path. Mega-1 wires `pkg/sched/admission.go::KindJob` so that
   `Σ(ram_mb + 8)` over live job instances is debited from the
   47,600 MB `RAMAdmissionCeiling` — same as wake, separately
   from `JobConcurrentPerAccount`. Documented in
   `docs/runbooks/FaasJobsQueueBacklog.md`.

4. **Vsock 1026 port overlap with characterize.** ADR-099 v1 said
   "reuse the vsock exit port"; Mega-1 confirms the port-number
   overlap with `VsockCharacterizationHostPort=1026` is
   intentional, discriminated by message type: characterize uses
   `msg_type=3`, while the guest-initiated job-exit stream uses `msg_type=4`.
   `pkg/fcvm/config.go::VsockJobExitPort=1026` +
   `VsockJobExitMsgType=4`.

5. **Lease primitive is local.** `pkg/sched/lease.go::Leaser[T]`
   defines the local API surface. **Mega-1 does NOT import
   `pkg/dispatch`** — the types mirror `pkg/dispatch.Leaser[T]`
   so ADR-134's post-Mega-1 refactor is a mechanical swap. ADR-099
   v1 left this decision open.

6. **Pg_notify payload is versioned.** Migration 00575 replaces
   the existing `job_tasks_notify_trg` with two channels
   (`job_tasks_dispatched`, `job_tasks_terminal`) carrying
   `{v:1, run_id, task_index, attempt, exit_code, error_class}`.
   Versioned so old listeners ignore new payloads and new
   listeners reject old ones.

7. **Pre-existing jobs without `command`.** Migration 00572
   adds `jobs.command text[]` with a fail-closed backfill
   (`echo "no command; PATCH your job"; exit 1`). Customers
   must PATCH before re-running. Documented in the runbook.

## Risks closed by this supplement

- **R1 (cold-boot wake storm on fan-out)** — closed by
  per-plan parallelism cap (table above) + separate
  `JobDispatch` bucket in `pkg/sched/rate_limit.go` so jobs
  cannot starve app wakes.
- **R2 (vsock port overlap)** — closed by STREAM/DGRAM
  discrimination (deviation 4).
- **R3 (job RAM starvation of app wakes)** — closed by
  `KindJob` admission wired through `RAMAdmissionCeiling`
  (deviation 3) + `JobConcurrentPerAccount` cap (table).
- **R4 (lease race during schedd restart)** — closed by
  reaper pre-CAS on `lease_expires_at > now()` (M6 reaper
  in `pkg/sched/reaper_jobs.go`).
- **R5 (pg_notify payload drift)** — closed by versioned
  payloads (deviation 6).
- **R6 (pre-existing jobs without command)** — closed by
  fail-closed backfill (deviation 7).

## Cross-references

- **ADR-134** (`worktree-feat-dispatch-contract-pr-a`) — the
  post-Mega-1 refactor that swaps `pkg/sched/lease.go::Leaser[T]`
  for `pkg/dispatch.Leaser[T]`. Surface parity preserved so the
  refactor is mechanical.
- **PR #916** — original jobs PR (open-draft, 1847 commits
  behind). Mega-1 cherry-picks-rebuilds it onto current main
  with the as-built table (deviation 2).
- **§17 G-Async-Retention + G-Account-Concurrency** — closed by
  PR #1185 (ADR-134 PR-A). Mega-1 inherits the closure.
