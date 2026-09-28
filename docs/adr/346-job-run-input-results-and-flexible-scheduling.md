# ADR-346: Job run inputs, results, and flexible scheduling

Status: accepted (2026-09-28).

## Context

ADR-099 established durable job definitions, runs, indexed tasks, bounded
parallelism, retry, cancellation, and retained log tails. A task currently has
no customer input identity, a retry replaces its prior result fields, and the
run record does not retain the effective job configuration. Cron scheduling and
capacity deferral do not give a customer an accepted execution window.

## Decision

1. A run captures its effective command, timeout, retry policy, task count,
   parallelism, and environment override values. Run-level arguments replace
   only the command's trailing arguments. The existing job-update fence keeps
   a job's image, RAM, and default environment fixed while it has queued or
   claimed tasks. Existing environment override rules remain in force.
2. A run may declare an ordered input set in its request. Each entry has a
   unique customer `input_id` and an opaque `input_ref`. The ordered set gets
   a version and checksum on the run; each binding is retained on its task.
   Task count is derived from the entries. Runs without inputs keep the
   existing numeric `tasks` contract. A task index is zero-based and stable
   across retries. Images retrieve input data using their own credentials.
3. Every job command receives platform-owned `GREGALE_RUN_ID`,
   `GREGALE_TASK_INDEX`, `GREGALE_TASK_ATTEMPT`, and `GREGALE_TASK_COUNT` values.
   Input runs also receive `GREGALE_INPUT_ID` and the input reference.
   Platform identity wins over customer environment overrides.
4. A task retains its current attempt number and result. A retry increments
   that number and clears the previous attempt's result fields. A successful
   task may publish a bounded output manifest containing customer-provided
   object references, sizes, and checksums. Large output stays in customer
   storage, not in PostgreSQL or the guest-exit envelope. The platform
   validates the manifest shape and only the current fenced attempt can
   commit it. The platform does not verify that each referenced object exists
   or that its contents match the claimed checksum.
5. `continue` remains the default failure policy: eligible tasks finish and
   the run exposes all per-input outcomes. `fail_fast` prevents new claims after
   a permanent failure and settles unstarted tasks as cancelled; running tasks
   may finish. Customers can inspect each task's final result and manually
   retry eligible failed tasks under the existing retry contract.
6. Flexible runs have an eligibility time and a latest-start time. The latter
   applies to each task: any task not admitted by that time expires, while
   already-running tasks follow their normal timeout. Flexible tasks use spare
   capacity only after standard jobs and the app-wake admission reserve.
   Acceptance does not promise completion by the latest-start time. Pricing
   remains unchanged until a separate metering and entitlement change lands.

## Ownership and rollout

`apid` validates customer intent and creates immutable run/input rows.
`schedd` owns task claims, attempt transitions, expiry, and result commitment.
`vmmd` transports per-task configuration and remains the only privileged VM
owner. Guest-init injects identity and ships bounded completion metadata.
The production job-dispatch gate remains `FAAS_JOBS_DISPATCH=1` in systemd.
Release qualification still requires native KVM acceptance for the guest
result channel and an operator recovery exercise.

## Consequences

The schema grows with run snapshots, input bindings, and result references.
Existing numeric runs and cron-created single-task runs
continue to work. Stale attempts cannot overwrite current results. A run can
end with useful successful outputs and an aggregate failure or expiry status.
