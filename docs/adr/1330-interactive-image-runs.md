# ADR-1330 · Interactive image runs (`gregale run -it --image`)

- **Status:** accepted for an operator-gated preview
- **Date:** 2026-10-15
- **Decision:** An interactive image run is an image run (ADR-1161) whose one
  task VM holds its command until one client attaches. The command then runs
  with a pseudo-terminal (or plain pipes) wired to that client. The session
  reuses ADR-1037 end to end: the one-time attach token, the WebSocket wire
  format, the gateway → vmmd `AttachAppTask` RPC and the guest's version-2
  session protocol on vsock 1031. guest-init's job supervisor keeps owning the
  task timeout, cancellation and exit report, so the task's outcome, retries,
  billing and run aggregate take the ordinary Job path.
- **Why:** `gregale run --image alpine sh` exits at once: a Job task's stdin
  is `/dev/null`. ADR-1161 deferred `-it` because the ADR-1037 attach
  transport targets app tasks only. Container users expect `docker run -it`.

## Contract

`POST /v1/image-runs` accepts `interactive: true` and an optional `tty`. apid
admits it only when `FAAS_INTERACTIVE_IMAGE_RUNS=1` (on top of
`FAAS_IMAGE_RUNS=1`). An interactive run is one task with no retries, and its
task timeout (1..3600 s) is the session limit. apid mints a 32-byte attach
token and stores only its SHA-256 digest in a `job_run_attach_sessions` row
inserted in the same transaction as the run, so schedd never claims the task
without seeing it. The create response returns the plaintext once, with the
attach path `/v1/jobs/{name}/runs/{id}/tasks/0/attach`. A failed interactive
run cannot be replayed or have its task retried (`POST .../replay-failed` and
`POST .../tasks/{idx}/retry` return 409): either would run the command again
in a new VM with no client, and the new VM would accept the spent token.

The lifecycle:

1. **schedd.** `WakeJob` reads the session row and passes `interactive {tty,
   attach_token_sha256, attach_timeout_seconds}` in `JobColdBootRequest`. vmmd
   echoes `interactive: true`. A vmmd that does not echo it predates this ADR;
   schedd rolls back the admission and fails the task instead of letting the
   command run without stdin. The exit watch adds the attach window to its
   deadline.
2. **vmmd.** It stages `interactive {tty, attach_timeout_s}` in the task's
   `job.json`, boots the VM, and registers ADR-1037's single-use rendezvous for
   the job instance before `JobColdBoot` returns. schedd publishes the
   instance as `running` only after that return, so a running instance always
   has a rendezvous unless a client already claimed it or its window closed.
   On attach, vmmd dials the guest's session port, retrying while guest-init
   starts. It relays stdin, resize and output as for app tasks, and sends the
   terminal result to the client. vmmd does not destroy the VM when the
   session ends; the job exit path does, as for every Job task.
3. **gatewayd-internal.** It serves `GET /v1/jobs/{name}/runs/{id}/tasks/{idx}/attach`.
   It applies the image-run route's auth (bearer or session, MFA,
   deploy-write scope, `JobsAllowed`) and resolves the task through the job
   name, scoped to the account. A terminal task is 410. A task that is not
   claimed, or whose instance is not yet `running` on a recorded node, is 409
   with `Retry-After: 1`. Otherwise it dials that node's vmmd `AttachAppTask`
   with the job instance and the presented token. `gatewayd-public` lists the
   path as compute-owned.
4. **guest-init.** In job mode with `interactive`, it does not start the
   command at boot. It accepts one session on vsock 1031 within the attach
   window, while still honouring host cancellation. The request must be
   version 2 and interactive. The guest ignores the request's command and runs
   the job's command from `job.json`, through the job supervisor, with the
   session's PTY or pipes as stdio. For a TTY it uses a private `devpts`
   instance and starts the command in a new session with the PTY as its
   controlling terminal. The supervisor applies the task timeout (SIGTERM,
   then SIGKILL after the grace period) and host cancellation. It reports the
   exit over vsock 1026 exactly as for batch tasks, and the session's terminal
   result carries the same exit code.

Outcomes:

| Event | Task status | Exit code | Client sees |
|---|---|---|---|
| Command exits | from the exit code, as for batch tasks | the command's | its exit code |
| Task timeout | `timeout` | 124 | `timed_out` |
| Run cancelled (Ctrl-C before attach, `jobs runs cancel`) | `cancelled` | 128 + signal | session ends |
| Client disconnects | `cancelled` | 129 (SIGHUP to the process group) | (gone) |
| No attach within the window (60 s) | `failed` | 125 | (gone) |

## Output and billing

No session bytes are stored, as in ADR-1037: terminal sessions routinely print
secrets. Stored output stays empty, and ADR-1163's live stream ends with
`stored_output`. Billing is the Job task's plan RAM + 8 MB per running second
from boot, including the attach wait, which the 60 s window bounds.

## CLI

`gregale run -it --image IMAGE [ARG...]` (`-i`, `-t`, `--interactive`,
`--tty`). `-t` needs `-i`; if stdin is not a terminal, the CLI warns and
continues without a tty, as `app exec -it` does. `--detach` and `--json` are
rejected. After the pull it opens the attach WebSocket, retrying 409 until the
VM is ready. It then relays stdin, window size and output like `app exec -it`
and exits with the command's exit code. Ctrl-C before the session starts
cancels the run; after it starts, Ctrl-C reaches the remote terminal as
`^C`.

## Gating and rollout

`FAAS_INTERACTIVE_IMAGE_RUNS=1` gates admission. Roll out vmmd, schedd, the
gateways and the platform base image (guest-init) first. Each older component
fails closed:

- **vmmd:** schedd sees no `interactive` echo and fails the task.
- **guest-init:** it decodes `job.json` with unknown fields disallowed, so it
  rejects the `interactive` field and reports an infrastructure failure
  instead of running the command without stdin.
- **gateways:** without the route the attach returns 404, and the CLI cancels
  the run.

The gate stays off by default until native acceptance
(`TestInteractiveImageRunMetal`) passes.

## Consequences

- An interactive run gets an image run's isolation: a fresh VM that is never
  routed, parked or snapshotted, and is destroyed when the command exits.
- The attach token is a bearer credential for one session. It is useless
  without the account credential the gateway also requires, and it is
  consumed on first use. There is no reattach.
- apid never stores a response that carries a one-time credential for
  `Idempotency-Key` replay (`httpkit.SkipIdempotentReplay`), so neither an
  interactive run's token nor an ADR-1037 task's token lands in the
  idempotency table. A retry with the same key creates a new run instead of
  replaying the old token. A 501 (a gate that is not enabled yet) is not
  stored either.
- `jobs logs` and the live stream show nothing for an interactive task. The
  task row records the command, timing, node and exit code.

## Rejected alternatives

- **An app task instead of a Job run:** app tasks need a deployment; an image
  run has none, and ADR-1161 deliberately reuses the Job path for scheduling,
  quotas and billing.
- **Attach to the already-running command's stdio:** the command would start
  and possibly exit before the client attached, and its early output would be
  lost. Holding the command until attach matches `docker run -it`.
- **Persist session output:** same reason as ADR-1037.
