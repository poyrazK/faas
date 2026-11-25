# MCP hosting canary

`.github/workflows/mcp-canary.yml` probes customer MCP hosting and the hosted
management MCP every hour from a GitHub-hosted runner
([ADR-20261123100100](../adr/20261123100100-mcp-hosting-synthetic-canary.md)).

## Activate

1. Create a project for the canary in the operator org, then an agent key
   limited to it:

   ```sh
   gregale org key create --org <org> mcp-canary \
     --scopes agent,deploy:write --approvals none --projects <project>
   ```

2. Add repository secrets:
   - `MCP_CANARY_KEY`: the key from step 1.
   - `CANARY_FALLBACK_WEBHOOK_URL`: shared with the deploy canary; failures post here.
   - `MCP_CANARY_HEARTBEAT_URL`: a separate success URL on the external
     missing-run monitor. Use a 1-hour interval with 30 minutes of grace.
3. Optional repository variable `MCP_CANARY_APP` (default `mcp-canary`).
4. Run the workflow by hand with **deploy** checked. The first run creates
   the app; later runs reuse it and redeploy every sixth hour.

## Read a failure

The job summary has one row per check. `flaky` rows passed on retry and only
warn. The JSON results are kept as a run artifact for 14 days.

| Check | First look |
|---|---|
| `deploy` | the CLI output tail in the detail column; then `gregale release logs` for the app |
| `discover`, `legacy`, `catalog`, `call` | the HTTP status in the detail; then the app's logs |
| `stream` | gateway streaming (`streaming_status` from `gregale mcp doctor`) |
| `long_call` | gateway restarts on the nodes, request timeout set on the app |
| `egress`, `wake_egress` | node egress and NAT; restored apps holding dead pooled sockets |
| `cross_node` | the failing entry IP names the node that cannot answer the other node's apps |
| `wake` | wake p95 on the dashboard; snapshot restore errors in `vmmd` |
| `mgmt_*` | `mcp-bridged` on the control plane and `api.gregale.dev` routing |

Run the same probe locally with a temporary key:

```sh
MCP_CANARY_KEY=... python3 scripts/ops/mcp_canary.py --app mcp-canary-dev --deploy
```

Delete a temporary app and revoke the key afterwards.
