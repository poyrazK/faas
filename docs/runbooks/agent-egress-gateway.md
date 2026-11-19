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

4. Ship the gateway's logs and metrics off the host (§Off-host logs and
   metrics below).
5. Turn on `FAAS_AGENT_EGRESS_GATEWAY` in vmmd on one node, then the fleet.

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

With off-host metrics on (§Off-host logs and metrics), the proxy serves them
on the gateway's `wg-obs` address instead (`ip -4 addr show wg-obs`), and the
control plane's Prometheus scrapes them as job `agentproxyd`.

From a sandbox session with web egress, fetch an IP-echo service. The answer
must be one of the agent addresses, and the same one on every request from
that session.

## Off-host logs and metrics

The gateway host is outside the private network. A `wg-obs` WireGuard tunnel
to the control plane carries its logs to Loki and lets Prometheus scrape it
(ADR-20261117185346, amendment 2026-11-19). The control plane dials the
gateway, so the control plane opens no internet port. The same tunnel serves
the tenant gateway (ADR-372), whether it shares the host or not.

1. Set the operator variables beside `faas_obs_logs_enabled`:
   `faas_gateway_obs_enabled: true`, and `faas_gateway_obs_tunnel_cidr` if
   `10.255.254.0/29` is taken on the control plane or a gateway. A gateway
   the control plane cannot reach at its `ansible_host` takes
   `faas_gateway_obs_endpoint` as a host variable.
2. Converge the control plane's `nftables`, `loki` and `prometheus` roles
   (its firewall then accepts Loki pushes on `wg-obs`, Loki keeps the
   connection log 30 days, and Prometheus has the gateway jobs).
3. Converge the agent gateway (`bootstrap-agent-egress-gateway`), so
   `agentproxyd` serves its metrics on the tunnel address.
4. Bring up the tunnel, the gateway's logs leaf and Alloy, and the scrape
   targets. Run it whole, without `--limit`:

   ```bash
   make ANSIBLE_INVENTORY=deploy/ansible/.generated/inventory/hosts.ini bootstrap-gateway-observability
   ```

5. The owner moves the Loki bucket's lifecycle rule from 16 to 32 days
   (docs/ops/logs.md). Until then Loki holds 16 days of the connection log.

Verify, on the control plane:

```bash
sudo wg show wg-obs latest-handshakes
```

```bash
sudo gregalectl obs logs --since 15m --query '{job="agent-egress-connections"}' --limit 5
```

Prometheus's targets page lists `promtail-egress-gateway` and `agentproxyd`
up. `pki_renew.yml` renews the gateway's leaf with the others.

If the tunnel is down, nothing on the gateway stops: `agentproxyd` and Alloy
bind the tunnel address whether or not it exists. Logs wait in the journal
and Alloy catches up when the tunnel returns; `FaasEgressGatewayUnreachable`
fires after 10 minutes.

## The connection log

`agentproxyd` writes one JSON line per connection, carried or refused, to its
own journal namespace on the gateway. The gateway keeps it 30 days
(`faas_agentproxyd_log_retention`), and with off-host logs on, Alloy ships it
to Loki as `{job="agent-egress-connections"}`, also kept 30 days. Only
`outcome` is a label; every other field is in the line.

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

Read it in Loki, on the control plane (times are RFC3339; the range is at
most 14 days per query):

```bash
sudo gregalectl obs logs --from 2026-11-18T10:00:00Z --to 2026-11-18T11:00:00Z --limit 5000 --json --query '{job="agent-egress-connections"}' | jq -c '.entries[].line | fromjson'
```

LogQL's `| json` names the line's `host` field `host_extracted`, because
`host` is already the stream's label (the gateway).

If Loki has no lines for the time (the tunnel was down longer than Alloy's
retries, or the report is older than Loki keeps), read the gateway's own
copy, the source of truth:

```bash
sudo journalctl --namespace=agentproxyd -o cat --since "2026-11-18 10:00" --until "2026-11-18 11:00"
```

## Abuse reports

An abuse desk reports "your address A contacted B at time T". If A is an agent
address, the connection was an agent's, never a customer app's.

1. On the control plane, find the connections from A to B around T in Loki:

   ```bash
   sudo gregalectl obs logs --from <T-15m> --to <T+15m> --limit 5000 --json --query '{job="agent-egress-connections"} | json | egress="<A>" | dest="<B>"' | jq -c '.entries[].line | fromjson'
   ```

   If the report names a domain rather than an address, filter on
   `host_extracted="<domain>"` instead of `dest`. Each line names the account
   and the sandbox. No lines, and Loki may not have them: run the same
   selection on the gateway's journal:

   ```bash
   sudo journalctl --namespace=agentproxyd -o cat --since "<T-15m>" --until "<T+15m>" | jq -c 'select(.egress == "<A>" and .dest == "<B>")'
   ```

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

No line for A, B and T: check the time zone of the report (`gregalectl obs
logs` takes RFC3339, journal times are the gateway's local time; `start` is
UTC). Traffic on ports other
than 80 and 443 does not pass through the proxy; for it, the flow log in step
2 is the only record. A refusal (`outcome` other than `accepted`) never
reached B.

## Symptoms

The alerts below are in `faas.rules.yml` (group `faas_egress_gateways`).

- **`FaasAgentProxyDown` (page).** Prometheus cannot scrape `agentproxyd`.
  Agent web egress is refused while the proxy is down. If
  `FaasEgressGatewayUnreachable` fires with it, the host or `wg-obs` is down,
  not the proxy: check the tunnel's handshake on the control plane first.
- **`FaasEgressGatewayUnreachable` (warn).** Prometheus cannot reach the
  gateway's Alloy. Check `sudo wg show wg-obs latest-handshakes` on the
  control plane, then `systemctl status alloy wg-quick@wg-obs` on the
  gateway. Its logs are safe in the gateway's journal meanwhile.
- **`FaasAgentEgressRefusalSpike` (warn).** More than 20 % of agent
  connections refused by caps, the blocklist or destination checks; see the
  `cap_*` entry below and split by outcome:
  `sum by (outcome) (rate(agent_egress_connections_total[5m]))`.
- **`FaasAgentEgressUnregisteredRefusals` (warn).** See the first entry
  below.

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
