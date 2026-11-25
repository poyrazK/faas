# Hosted management MCP server

Operational owner: the apid owner. `mcp-bridged` (`cmd/mcp-bridged`,
`pkg/mcpmanagement`) serves `https://api.<platform domain>/mcp` on the control
plane, calls apid for every tool, and ships in the control-plane release
lane. The capability is a preview, so its alerts warn and never page
(escalation: [the matrix](../ops/escalation.md)). ADR:
[ADR-20261026195952](../adr/20261026195952-hosted-management-mcp.md).

Covers `FaasHostedMCPDown`, `FaasHostedMCPRestarting` and
`FaasHostedMCPConnectionsNearCapacity` (all warn).

## How it runs

- **Unit:** `faas-mcp-bridge`, user `faas-mcp-bridge`, listening on
  `127.0.0.1:8787`. gatewayd-public routes `/mcp`, `/mcp/*` and the OAuth
  metadata paths on the `api.` host to it.
- **Metrics:** `127.0.0.1:9110/metrics` (Prometheus job `mcp-bridged`). The
  `gregale_management_mcp_*` series carry no account, grant or app labels.
- **State:** clients, sign-ins, grants and plans are sealed rows in
  Postgres (`mcp_bridge_state`) under a lease (`mcp_bridge_leases`, 30 s,
  renewed every 10 s). The key is `/etc/faas/secrets/mcp-bridge/state.key`.
- **Exits on purpose** when it loses the lease or Postgres refuses a state
  write, so memory never runs ahead of what committed. systemd restarts it
  (`Restart=on-failure`) and it reloads the committed state. Agents keep
  their connections.
- **Memory:** a connection's MCP server is built on its first request and
  dropped after ten idle minutes, so memory follows active connections.
  `gregale_management_mcp_connection_servers` counts the built ones.

## Run a controlled restart

Dispatch [ops-restart-hosted-mcp](https://github.com/poyrazK/faas/actions/workflows/ops-restart-hosted-mcp.yml)
from `main` when the `production-us` fleet rollout is idle. It restarts
`faas-mcp-bridge.service` on the control plane and reports the old and new
systemd invocation IDs once the new invocation answers on loopback.

For an upload approval rehearsal, record the pending plan ID, approval ID,
and upload ID before dispatch. After the restart, confirm that hosted MCP
still returns the same plan and approval. Have the reviewer approve using the
original review link, then retry the same plan ID.

## OAuth client callback refused

`invalid_client` with `none of the client's redirect URIs is allowed on this
server` means the bridge fetched the client's metadata but none of its callbacks
matched the operator allowlist. Authorization stops before account sign-in.

The shipped unit allows Google Antigravity's exact HTTPS callback,
`https://antigravity.google/oauth-callback`, published at
`https://antigravity.google/oauth/client-metadata.json`. Older units omit it.
Inspect `systemctl cat faas-mcp-bridge` for the active `--oauth-redirect-uri`
arguments and any `ExecStart` override. Converge `control_plane_service` to
install the updated unit and let its handler reload/restart the bridge. An
override that replaces `ExecStart` also needs the exact callback. Retry
**Authenticate** in Antigravity after the updated service is running.

Add only independently verified exact HTTPS callbacks; the existing loopback
rules remain separate. Do not accept arbitrary client-provided redirect hosts.

## Inspect and revoke connections

The admin socket is `/run/faas-mcp-bridge/admin.sock`, in a 0700 directory
the service user owns. Run the CLI as that user:

```sh
sudo -u faas-mcp-bridge /opt/faas/current/bin/gregale mcp connection list \
  --socket /run/faas-mcp-bridge/admin.sock --limit 100
sudo -u faas-mcp-bridge /opt/faas/current/bin/gregale mcp connection revoke \
  --socket /run/faas-mcp-bridge/admin.sock --grant-id <grant_id>
```

The list shows each connection's grant, client and account IDs, scopes,
expiry and consented apps and environments; never tokens or keys. To cut off
one account, revoke each grant whose `account_id` matches (at most ten).
Revocation stops the grant's requests, watches and subscriptions at once and
commits before it reports `durable: true`. The agent has to sign in and
consent again. Use it for a leaked agent, an abusive client, or to free
capacity.

## Alerts

### FaasHostedMCPDown

Prometheus has not scraped `mcp-bridged` for 5 minutes. Agents get errors
from `api.<platform domain>/mcp`; the rest of the platform is unaffected.

1. `systemctl status faas-mcp-bridge`. systemd stops restarting it after
   five failures in a minute: fix the cause, then
   `systemctl reset-failed faas-mcp-bridge` and start it.
2. Read `journalctl -u faas-mcp-bridge`. Common causes:

| Journal says | Cause and fix |
|---|---|
| `MCP state lease lost to another process` | Another `mcp-bridged` holds the lease, e.g. the old primary after a control-plane failover still runs. Stop the unit on the host that should not serve. |
| `MCP state could not be committed` | Postgres refused a write: down, failing over, or out of connections (the `mcp-bridge` pool budget is 2). Fix Postgres; the restart reloads. |
| `MCP state is held by another running bridge` | A start waited 65 s for the lease. A crashed predecessor's lapses within 30 s, so another live process holds it: find it with the query below and stop it. |
| `MCP state key` | `/etc/faas/secrets/mcp-bridge/state.key` is missing or malformed. Converge `control_plane_service`; it generates the key once. |
| `discarded hosted MCP state` (warning, not an exit) | The key changed, so the stored state could not be opened and was dropped. Agents sign in again. |

3. If the unit is not installed or the binary is missing, the release that
   ships `mcp-bridged` has not been converged on this host yet: converge
   `control_plane_service` and restart the unit after activation.

To check the lease without changing it:

```sql
SELECT bridge, holder, epoch, expires_at, now() AS db_now
  FROM mcp_bridge_leases;
```

### FaasHostedMCPRestarting

The process started at least three times in 30 minutes. After a crash,
each start waits up to a minute for the old lease to lapse, and agents'
requests fail meanwhile. The journal before each start names the cause; use
the table above. Repeated `MCP state could not be committed` with Postgres
healthy can mean a state row over its 32 MiB bound: report it with the
journal lines, since the bound is a code change.

### FaasHostedMCPConnectionsNearCapacity

The bridge holds at least 90% of one kind of authorization entry for 30
minutes. The bound is 2,000 each of clients, pending sign-ins, codes and
grants. At the bound, new client registrations or sign-ins are refused; the
`kind` label names which.

- **grants:** list connections and look for accounts or clients with many
  grants (the per-account bound is ten). Revoke abandoned ones. A grant expires
  eight hours after consent.
- **clients:** dynamic registrations expire on their own; at most 20 per
  address per hour. A steady climb from one source is abuse: block it at the
  edge.
- **requests** and **codes** expire within minutes; a sustained high count
  means sign-ins are started and not finished, often a broken client.

Raising the bound is a code change (`hostedOAuthEntries` in
`pkg/mcpmanagement/hosted_limits.go`) and an ADR amendment.

## Optional Task recovery inspection

`--allow-task-inspection` defaults off. With `build-tools` selected it exposes
only `gregale_inspect_task_tool_recovery` and `gregale_plan_task_tool_recovery`
to the original personal-account agent key. It is independent of
`--allow-tool-publishing`, invocation and connection write consent, and needs no
CLI runner or upload store. The key must retain `agent,apps:read,deploy:write`
for the existing native requester check. OAuth, organization/project-limited
and app-bound credentials remain excluded; another key cannot read the journal.
See the [customer workflow](../mcp.md#hosted-read-only-task-inspection) and
[ADR-20261116163708](../adr/20261116163708-hosted-task-recovery-inspection.md).
Disabling the flag on bridge restart removes the tools; it never changes native
release state, approvals or running executors. Do not retry a release to recover
an inspection response: reconnect and inspect its retained intent ID.
