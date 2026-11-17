# ADR-20261111150000: Agent approval for the developer debugger tunnel

Date: 2026-11-11
Status: Accepted
Amends: ADR-20261104140058 (human approval for risky agent actions),
ADR-20261107220000 (agent activity log)
Related: ADR-741 (`gregale dev --debug`), ADR 20261029202403 (gateways ask
apid to resolve credentials)

## Context

`gregale dev --debug` opens a WebSocket tunnel from the CLI through
gatewayd-internal to the Node inspector of a running developer environment
(`GET /v1/apps/{slug}/debug`). A debugger session evaluates code in the app
and reads its process environment, which holds the app's secrets. An agent
key reaches it with no person's say, and leaves no row in the agent activity
log:

- `agentapproval.Classify` exempts every `GET`, so the tunnel is never gated.
- The activity log records only writes (`POST`, `PUT`, `PATCH`, `DELETE`).
- The tunnel is served by gatewayd-internal, and apid is the only writer of
  approval requests and of the activity log, so the gateway could not gate
  or record it even if the route were classified.

An approval allows exactly one request. A debugger opens one tunnel per
connection, and the CLI's discovery cache opens one about every 2 s while a
debugger polls for targets, so a per-request approval would make the
debugger unusable for agents.

## Decision

1. **The tunnel needs a credentials approval.** `GET /v1/apps/{}/debug` is a
   *gated read*: it is classified `credentials` like minting a key, before
   the `GET` exemption. Other reads stay ungated.
2. **One approval covers a window.** A rule may carry a window. For the
   tunnel it is `api.DevDebugApprovalWindow` (1 hour). Once an approval with
   the request's binding (key, method, path) has been used, every identical
   request within the window of that first use goes through on it. Past the
   window the next attach asks a person again. Another app's tunnel is
   another binding, so it needs its own approval. Rules without a window are
   unchanged: one approval, one request.
3. **Every attach is in the activity log.** A gated read is recorded like a
   write. A WebSocket handler returns only when the connection ends, so the
   row is written when the handler takes the connection over, with status
   101 and the approval it went through on.
4. **apid decides for the gateway.** The Auth gRPC service gains
   `DecideAgentRequest` and `RecordAgentActivity`. The gateway's auth
   middleware sends a gated agent request's route pattern and binding.
   apid loads the key on the account, classifies the pattern itself (so a
   gateway cannot pick a laxer rule), and runs the same gate the HTTP API
   uses. apid answers allow or the RFC 7807 problem the gateway relays.
   Activity rows go the same way. apid stays the only writer of both
   tables.
5. **Fail closed.** When apid cannot decide (unreachable, or a release
   without the RPC during a rollout), the gateway answers 503 to the agent
   key's tunnel. Keys that are not agent or mcp-login keys never reach the
   gate. The deploy order (control plane before edge) ships the RPC first.
6. **The CLI names the link.** A refused tunnel handshake with
   `428 approval_required` is reported with the review link and the window.
   Repeated attaches while the request is pending reuse the same request.

## Consequences

- An agent can debug a developer environment for an hour on one approval,
  and a person sees what it attached to in the activity log.
- The gateway makes one extra unary RPC per agent tunnel. Human keys and
  sessions are unaffected.
- `AgentApprovalUsedWithin` reads consumed approvals by key and binding
  through the existing `(key_id, created_at)` index. Approvals are pruned a
  week after expiry, well past the window.
