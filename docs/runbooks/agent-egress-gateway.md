# Agent egress gateway (ADR-20261117185346)

Agent VMs (sandbox sessions and agent runs with egress) leave through their
own WireGuard gateway and their own public addresses, apart from the tenant
gateway's. With `egress.agent_gateway.proxy` on, agent TCP 80 and 443 pass
through `agentproxyd`, the politeness proxy, which caps connections, refuses
blocklisted domains and logs every connection. This runbook applies to fleets
whose manifest declares `egress.agent_gateway`. To take production from no
gateway to web and browser sessions, follow
[agent-egress-production-rollout.md](agent-egress-production-rollout.md).

## Bring-up

1. The owner assigns the agent public addresses to the gateway host. They
   carry only agent traffic.
2. Add `egress.agent_gateway` to the manifest (`endpoint`, `public_key`,
   `tunnel_cidr`, `addresses`, optional `node_prefix`, `ssh_host`, `proxy`)
   and regenerate the inventory:

   ```bash
   make manifest-ansible MANIFEST=deploy/manifest/splitbox.yaml
   ```

3. Converge the gateway, then every compute node. With `proxy: true`, pass a
   signed release that contains `agentproxyd`
   (`faas_agent_egress_release_tarball`, `_signature`, `_git_sha`,
   `faas_agent_egress_cosign_source`); the converge fails without one, because
   the firewall already sends agent 80 and 443 to the proxy.

   ```bash
   make ANSIBLE_INVENTORY=deploy/ansible/.generated/inventory/hosts.ini bootstrap-agent-egress-gateway
   ```

4. Turn on `FAAS_AGENT_EGRESS_GATEWAY` in vmmd on one node, then the fleet.

## Verify

On a compute node:

```bash
sudo wg show wg-agent latest-handshakes
```

On the gateway:

```bash
systemctl status faas-agentproxyd
```

```bash
curl -s 127.0.0.1:9473/metrics | grep agent_egress_
```

From a sandbox session with web egress, fetch an IP-echo service. The answer
must be one of the agent addresses, and the same one on every request from
that session.

## The connection log

`agentproxyd` writes one JSON line per connection, carried or refused, to its
own journal namespace on the gateway. It is kept 30 days
(`faas_agentproxyd_log_retention`) and stays on the gateway: no shipper
carries it off the host yet.

| Field | Meaning |
|---|---|
| `msg` | always `agent_egress_connection` |
| `outcome` | `accepted`, or why it was refused: `cap_vm`, `cap_account_domain`, `cap_domain_fleet`, `blocklist`, `unregistered`, `no_sni`, `bad_destination`, `upstream_error` |
| `start`, `duration_ms` | when the connection arrived, how long it lasted |
| `account_id`, `sandbox_id`, `node` | who made it; absent for `unregistered` |
| `source` | the VM's tunnel address and port |
| `host` | the TLS SNI or HTTP `Host` name |
| `dest`, `port` | the address and port the VM dialed |
| `egress` | the agent public address it left from; present only once the proxy dialed |
| `bytes_up`, `bytes_down` | bytes VM → destination and back |

Read it on the gateway:

```bash
sudo journalctl --namespace=agentproxyd -o cat --since "2026-11-18 10:00" --until "2026-11-18 11:00"
```

## Abuse reports

An abuse desk reports "your address A contacted B at time T". If A is an agent
address, the connection was an agent's, never a customer app's.

1. On the gateway, find the connections from A to B around T:

   ```bash
   sudo journalctl --namespace=agentproxyd -o cat --since "<T-15m>" --until "<T+15m>" | jq -c 'select(.egress == "<A>" and .dest == "<B>")'
   ```

   If the report names a domain rather than an address, select on `.host`
   instead. Each line names the account and the sandbox.
2. Confirm with the node's side. The egress flow log (ADR-371) has the VM's
   flows to B:

   ```bash
   gregalectl accounts egress-flows --remote <B> --from <T-15m> --to <T+15m>
   ```

   The session's reached-domain log (ADR-20261117091026 §4) lists the domains
   the session resolved.
3. Act on the account. For abuse, hold it:

   ```bash
   gregalectl accounts abuse-hold place --account-id <account_uuid> --note "<report reference>" --yes
   ```

   Then follow [tenant-abuse.md](tenant-abuse.md) §Recover.
4. If the destination asked not to be contacted by agents at all, add its
   registrable domain to
   `deploy/ansible/roles/agent_egress_gateway/files/blocklist.txt` in a PR and
   converge the gateway. The proxy reloads the list without a restart and
   refuses that domain for every agent.
5. Reply to the report with the action taken.

No line for A, B and T: check the time zone of the report (journal times
above are the gateway's local time; `start` is UTC). Traffic on ports other
than 80 and 443 does not pass through the proxy; for it, the flow log in step
2 is the only record. A refusal (`outcome` other than `accepted`) never
reached B.

## Symptoms

- **Agent sessions cannot reach any site, `unregistered` refusals rise.**
  vmmd's registrations are not arriving. On the node, look in vmmd's journal
  for `agent egress registration renewal failed`, and check `wg-agent`'s
  handshake. A registration lapses five minutes after its last renewal.
- **`cap_*` refusals rise.** An agent or account is opening more concurrent
  connections than the caps in `pkg/api/limits.go` allow. A single session
  hitting `cap_vm` is usually a runaway crawler. A domain hitting
  `cap_domain_fleet` is fleet-wide load on one site; that cap is what keeps
  agents polite.
- **`faas-agentproxyd` is down.** Agent TCP 80 and 443 are refused while it
  is down; agents fail closed, never around the proxy. `systemctl status
  faas-agentproxyd` and `journalctl --namespace=agentproxyd -u
  faas-agentproxyd` show why.
