# ADR-958 · Interactive app-task attach (`gregale app exec -it`)

- **Status:** accepted for an operator-gated preview
- **Date:** 2026-10-10
- **Decision:** Extend deployment-attached app tasks (ADR-230) with an
  interactive mode. An interactive task is an ordinary manual app task whose
  fresh VM runs one process with a pseudo-terminal (or plain pipes) whose
  stdin, stdout, window size and lifetime are driven by one attached client.
  The client's bytes travel CLI → WebSocket → `gatewayd-public` →
  `gatewayd-internal` → the owning node's vmmd (`AttachAppTask` bidi RPC) →
  vsock → guest-init. `schedd` keeps owning the task lifecycle unchanged.
- **Why:** `gregale app exec` runs one non-interactive command and returns a
  bounded output tail. Container users debug with `docker exec -it`: a shell
  with the deployed files, environment, secrets and bindings. ADR-230 left
  interactive PTYs as an explicit follow-up on the same primitive.

## Contract

`POST /v1/apps/{slug}/tasks` accepts `interactive: true` and optional `tty`.
apid admits a manual task as before and additionally mints a 32-byte random
attach token. Only its SHA-256 digest is stored (`app_tasks.attach_token_sha256`);
the plaintext is returned once in the create response and never again. The
command defaults to `/bin/sh`. Interactive tasks persist no output: their
stdout/stderr tails stay empty and their output budget is ignored. The task
timeout is the maximum session length (1..3600 s).

The lifecycle is ADR-230's. After restore, `schedd` marks the task `running`,
records the node that owns the VM (`attach_node_id`), and calls vmmd's existing
`ExecuteAppTaskStream` with the interactive fields and the token digest. vmmd
does not dial the guest yet. It registers a single-use rendezvous for the task
instance and waits up to the attach window (60 s) for a client:

- `gatewayd-internal` serves `GET /v1/apps/{slug}/tasks/{id}/attach` (a
  WebSocket, subprotocol `gregale-app-task-attach-v1`). It applies the shared
  auth chain (bearer/session, MFA, deploy-write scope, IDOR-safe app load),
  requires an interactive task of that app in `running` with a recorded node,
  and dials that node's vmmd `AttachAppTask` with the presented token.
  `gatewayd-public` lists the path as compute-owned so apid never sees it.
- vmmd compares the token digest in constant time and consumes the rendezvous
  on the first valid attach. Later attaches fail; there is no reattach.
- The first attach frame carries the terminal size. vmmd then dials the guest
  and sends a version-2 app-task request (`interactive`, `tty`, `rows`,
  `cols`). Stdin, resize and stdin-close frames flow host → guest; output and a
  terminal result flow back. The terminal result (status and exit code only)
  is sent to the client and returned to `schedd`, which completes the task.
- No attach within the window fails the task with `attach_timeout`. Client
  disconnect closes the guest channel; guest-init hangs up the session's
  process group and the VM is torn down exactly as for batch tasks
  (`client_disconnected`).

Guest-init mounts a private `devpts` instance only for interactive TTY tasks,
starts the process in a new session with the PTY as its controlling terminal,
as the deployment's configured user, with the same environment, secrets and
loopback bindings as batch tasks. A version-1 guest rejects the version-2
request, so an old base image fails closed with a redeploy hint instead of
running a shell without stdin.

## Gating

apid admits interactive tasks only when `FAAS_INTERACTIVE_APP_TASKS=1` and the
app-task API is enabled. The gateway endpoint rejects non-interactive tasks.
The capability is `internal` until native attach acceptance runs.

Roll out vmmd, schedd and the gateways before enabling the apid gate. A vmmd
without this change ignores the interactive fields and runs the command as a
batch task with no stdin; the shell exits immediately and the client sees
`Session ended` (410). Guests need the new guest-init (a redeploy) for the
version-2 request.

## Consequences

- Shell sessions get the same isolation as one-off commands: a fresh VM that is
  never routed, parked, pooled or snapshotted, and is destroyed afterwards.
- No session bytes are stored by Gregale. The task row records who started the
  session (audit), the command, timing, node and exit code.
- An attach token is a bearer credential for one session of at most the attach
  window; it is useless without the account credential that the gateway also
  requires, and it is consumed on first use.
- apid stays control-plane only; customer bytes take the established
  gateway → vmmd path used by HTTP, TCP and UDP ingress.

## Follow-ups

- **Live serving instances (`--instance`).** ADR-230 rejected running commands
  inside serving VMs. Attaching to one for debugging needs its own ADR (guest
  listener on serving instances, park/snapshot exclusion while attached,
  production access policy). It can reuse this ADR's token, gateway endpoint
  and `AttachAppTask` transport by targeting a serving instance instead of a
  task instance.
- `gregale cp` and port-forward can reuse the attach transport.

## Rejected alternatives

- **Proxy through apid:** apid must not call schedd or vmmd (component
  ownership, depguard `apid-control-plane-only`).
- **Proxy through schedd:** schedd would need a public-facing stream surface and
  every byte would take an extra hop; gateways already reach any node's vmmd.
- **Persist session output:** terminal sessions routinely print secrets; storing
  them would create a new sensitive data class for little debugging value.
