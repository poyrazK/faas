# ADR-1163 · Live job task output

- **Status:** accepted
- **Date:** 2026-10-14
- **Decision:** Add `GET /v1/jobs/{name}/runs/{id}/tasks/{idx}/logs/stream`,
  an SSE stream of one job task's output while it runs. The compute gateway
  (`gatewayd-internal`) serves it: it resolves the task's current instance and
  node from Postgres and follows that node's vmmd `Logs` RPC from the ring's
  first line. `gregale run --image` (ADR-1161) prints output as it is written,
  and `gregale jobs logs --follow` follows any task. This delivers the
  `--follow` that ADR-099 specified and never shipped.
- **Why:** A job task's console already lands in vmmd's per-instance ring
  from boot, and vmmd's `Logs` RPC can already follow it. Only the copy that
  schedd takes when the task exits reached the API, so `run --image` printed
  nothing until the command finished.

## Contract

- **Route:** served by the compute gateway, carved out of the apid proxy like
  ADR-1037's attach route; apid never dials vmmd. Auth requires a read scope,
  and the plan must include Jobs. The job, run and task must belong to the
  caller (`JobTaskLogTarget` joins jobs → job_runs → job_tasks → instances
  under the account).
- **Lifecycle:**
  - While the task is queued, the stream sends heartbeat comments.
  - It relays each attempt's VM in turn. A retry starts a new VM, a new ring
    and new sequence numbers; `instance` distinguishes them.
  - A task row names its instance before vmmd has booted the VM, so a
    missing ring is retried until the VM appears. A broken vmmd stream
    resumes after the last relayed line.
  - The stream ends once the task is terminal and its last VM's ring has
    closed.
  - `since_seq` resumes within the current attempt; 0 replays the attempt
    from its first retained line.
- **Frames:**
  - The same `log` and `gap` frames, and heartbeat comments, as the app log
    stream.
  - One final `end` frame. Its reason is one of:
    - `task_finished`: the task is terminal and its last VM's output was
      relayed.
    - `stored_output`: the task finished before its last VM's output could
      be streamed; read `GET .../logs`.
    - `timeout`: the 3 h backstop elapsed.
    - `node_unreachable` or `not_found`.
  - Heartbeats are also sent between lines, so quiet tasks survive proxy idle
    timeouts.
- **Stored output is unchanged:** schedd still copies the last 1 MiB to
  `job_tasks.log_content` at exit, and `GET .../logs` serves it.

## CLI

- **`gregale run --image`:**
  - Without `--json`, it streams lines as they arrive; stderr-tagged lines go
    to stderr.
  - It doesn't print the stored copy again after a complete stream.
  - If the stream can't open, as with an older gateway, or relays nothing
    before `stored_output`, it prints the stored output as before.
  - If the stream breaks part-way, it warns and names `gregale jobs logs`
    instead of printing lines twice.
  - Exit codes are unchanged.
- **`gregale jobs logs JOB RUN IDX --follow`:** streams until the task
  finishes. It cannot be combined with `--attempt`, `--max-bytes` or `--json`.

## Consequences

- vmmd currently tags all console output `stdout`, so stderr is not
  separated on the live path or in stored output. Splitting them needs a
  guest-side channel and is out of scope.
- Lines longer than the SSE decoder's 64 KiB buffer end the CLI's live
  stream; the CLI then warns and points at the stored output.
- Output is live only while the VM runs. A stream opened after the task
  ended returns `stored_output` at once, and the stored output is the
  record.

## Rejected alternatives

- **A schedd `StreamJobTaskLogs` RPC:** schedd's existing log RPC is keyed by
  app, and job VMs have no app. A new hop through schedd would add a daemon
  to the path without adding any decision. The gateway already dials
  task nodes' vmmd for ADR-1037.
- **Writing output to `job_tasks.log_content` incrementally:** it turns every
  console line into Postgres write load, and the column is a capped tail
  that cannot serve an append-only cursor.
