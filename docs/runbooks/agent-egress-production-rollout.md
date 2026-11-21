# Agent egress in production: from no gateway to web and browser sessions

Sandbox `web` mode (ADR-20261112121456) and browser sessions
(ADR-20261116182615, ADR-20261117065818) need an egress gateway. Production
had none on 2026-11-19: no compute node had `wg-tenant` or `wg-agent`, and
vmmd had no `FAAS_TENANT_EGRESS_IFACE`. Because web mode fails closed, it
cannot be turned on until the gateway exists.

This runbook covers the steps in order. For how each gateway works and how to
debug it, see [tenant-egress-gateway.md](tenant-egress-gateway.md) and
[agent-egress-gateway.md](agent-egress-gateway.md).

| Step | Who | Customer-visible change |
|---|---|---|
| 1. Decide host, addresses, ADR | owner | none |
| 2. Tenant gateway | operator | **every app's egress address changes** |
| 3. Agent gateway beside it | operator | none (switch off) |
| 4. Release with `agentproxyd` | operator | none |
| 5. vmmd agent switch, one node, then fleet | operator | sandbox egress moves to agent addresses |
| 6. Web mode, owner test | owner + operator | `network.mode: web` admitted |
| 7. Browser sessions, owner test | owner + operator | `browser: true` admitted |
| 8. Customer docs | operator | docs say it is available |

Each step can be backed out on its own (see "Backing out").

## 1. Owner decisions

- **Gateway host.** A small Linux VM with a public IPv4 address. Its provider's
  terms must allow multi-tenant egress. One host carries both gateways
  (ADR-20261117185346 §1, "same host, own addresses"). It is one failure
  domain: if it goes down, apps and agents lose egress together, with no
  fallback.
- **Agent addresses.** One or more extra public IPv4 addresses, bound as
  secondary addresses on the gateway's public NIC. They carry only agent
  traffic. Never rotate them to get around a block.
- **Accept ADR-20261117185346.** It is still `proposed`. Its §4 rollout
  condition (metal check) is met: see "Evidence" below.
- **Which fleet.** Edit the manifest the fleet is deployed from. After the OVH
  cutover that is `deploy/manifest/production/ovh-live.template.yaml`.

## 2. Tenant gateway

Follow [tenant-egress-gateway.md](tenant-egress-gateway.md) "Bring-up". Prod
specifics:

- Pick a `tunnel_cidr` that overlaps none of: the vRack
  (`10.20.0.0/24`), the tenant bridges (`10.100.0.0/16`), or the agent tunnel
  you pick in step 3.
- **This moves every customer app's IPv4 egress** to the gateway's address
  the moment each compute node converges. Customers who allowlist our egress
  address will break. Announce the new address before the converge.
- Converge the gateway first, then compute (the runbook's "Order matters").
- Verify on each node, then from a test app fetch an IP-echo service. The
  answer must be the gateway address.

## 3. Agent gateway on the same host

Follow [agent-egress-gateway.md](agent-egress-gateway.md) "Bring-up" steps 1–3.
The manifest stanza:

```yaml
egress:
  agent_gateway:
    endpoint: <gateway host>:51821          # its own port, not wg-tenant's
    public_key: <wg-agent public key>       # its own key, not wg-tenant's
    tunnel_cidr: 10.78.0.0/16               # overlaps neither tunnel_cidr above nor 10.100/16
    node_prefix: 22                         # one slice per compute node
    addresses: [<agent IPv4>, ...]          # from step 1, on the gateway NIC
    proxy: true
    ssh_host: <gateway host>
```

With `proxy: true` the converge needs a signed release that contains
`agentproxyd` (any release cut after #7584). Without one it fails on purpose,
because the firewall already sends agent ports 80 and 443 to the proxy.

After the converge, `vmmd` still has `FAAS_AGENT_EGRESS_GATEWAY` off, so
sandboxes keep their old path.

## 4. Release

The release must carry vmmd, schedd, agentd and apid from after:

- #7584 (agent gateway, proxy registration);
- #7513 (sandbox egress allow/deny);
- #7117 and #7122 (web mode, Chromium in agent-base);
- the browser stack #7464–#7509.

Release in the usual deploy order (`docs/release-compatibility.md`). apid's
switches stay off, so nothing new is admitted yet.

## 5. vmmd agent switch: one node, then the fleet

On one agent-pool node, add a drop-in, following the `60-agents.conf` pattern
in [AgentsDogfood.md](AgentsDogfood.md):

```bash
sudo mkdir -p /etc/systemd/system/faas-vmmd.service.d
```

```bash
printf '[Service]\nEnvironment="FAAS_AGENT_EGRESS_GATEWAY=1"\n' | sudo tee /etc/systemd/system/faas-vmmd.service.d/60-agent-egress.conf
```

```bash
sudo systemctl daemon-reload && sudo systemctl restart faas-vmmd
```

vmmd refuses to start if the node lacks `FAAS_AGENT_EGRESS_IFACE` or
`FAAS_AGENT_EGRESS_SLICE`. If it refuses, re-run `bootstrap-compute` on that
node; don't remove the check.

Verify on that node:

- `sudo wg show wg-agent latest-handshakes` is under 180 s.
- `ip rule show` shows priority 95 (mark → table 51821) and 96 (unreachable).
- Start a `registries`-mode sandbox (already admitted). Its `pip install` or
  `git clone` works.
- On the gateway, its connections log as `accepted` with the right account
  and sandbox:
  `sudo journalctl --namespace=agentproxyd -o cat -n 20`.
- `agent_egress_connections_total{outcome="unregistered"}` stays flat.

Leave it a day, then roll the drop-in to every agent-pool node. Move it into
the manifest or env contract only once it has run clean.

## 6. Web mode

The gating rule for customer features applies: the owner tests it in
production before customers can use it.

1. On the control plane, add `FAAS_SANDBOX_WEB_NETWORK=1` to apid in a drop-in
   (`60-sandbox-web.conf`) and restart apid.
2. Owner test, from the owner's own account:
   - Create a sandbox with `network: {mode: web}`, fetch an IP-echo service.
     The answer must be one of the agent addresses, the same on every request.
   - `curl https://example.com` works.
   - With `egress: {deny: [example.com]}`, the same fetch fails, and the
     session's domain log shows the refusal.
   - SMTP (25) and `169.254.169.254` are unreachable.
   - On the gateway, the proxy log has a line per connection naming the
     session.
3. If the test fails, remove the drop-in and restart apid. Sessions already
   running keep their mode until they end.

## 7. Browser sessions

Same pattern: `FAAS_SANDBOX_BROWSER=1` on apid (`60-sandbox-browser.conf`).
It needs web mode on. Owner test:

- Create a sandbox with `browser: true` (Pro and up). It reaches ready.
- `gregale-browser fetch -- https://example.com` returns JSON with the page
  title and text.
- An interactive session (`open`, screenshots) works from the dashboard or
  the SDK.

## 8. Customer docs

Once steps 6 and 7 pass, update `docs/sandbox-migration.md`. Change the
"Open internet egress", "Headless browser agents" and `web` rows from "Not
yet" to available, and name the plan floor. The docs sync publishes them to
gregale.dev/docs.

## Backing out

| Undo | How | Effect |
|---|---|---|
| Browser | remove `60-sandbox-browser.conf`, restart apid | new browser sessions refused |
| Web mode | remove `60-sandbox-web.conf`, restart apid | new `web` sessions refused |
| Agent path | remove `60-agent-egress.conf`, restart vmmd | sandboxes go back to the tenant path |
| Agent gateway | remove `egress.agent_gateway`, regenerate, re-run `bootstrap-compute` | `wg-agent` gone from nodes |
| Tenant gateway | [tenant-egress-gateway.md](tenant-egress-gateway.md) "Turning it off" | apps leave from node addresses again |

Undo in the order of this table. Web mode must be off before the tenant
gateway goes away, or web sessions fail closed.

## Evidence

Metal checks on gregale-internal-test-1, 2026-11-19 (#7620):

- `pkg/agentproxy`, `TestMetalAgentEgressGatewayPath`: the real node policy
  and gateway nftables template plus `agentproxyd`, over a tunnel with no
  `wg` tool (a veth). Checks registration, HTTP/TLS/raw ports, the connection
  log, blocklist and port refusals, per-VM peers, and no leak with the tunnel
  down.
- `pkg/fcvm`, `TestMetalSandboxWebEgressPolicy` and
  `TestMetalSandboxBrowserReadsAGrantedPage`: grant-only egress, deny policy,
  the domain log, and Chromium reading a granted page.
- `pkg/agentproxy`, `TestMetalAgentEgressGatewayCaps`: each of the proxy's
  three concurrent-connection caps filled with held connections, the next one
  refused with its outcome, other domains and accounts still carried, and
  slots returned once connections close.

Still owed before or during step 5:

- A real WireGuard handshake: the test host has no `wg`, so step 5's
  handshake check is the first one.
- Shipping the proxy log off the gateway host. Until then the log lives only
  on the gateway for 30 days.
