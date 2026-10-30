# Gregale Agents dogfood: PR triage on production (Milestone A)

This runbook turns Gregale Agents on for one account, Gregale's own, and one
agent-pool compute node. A pull request opened on one of that account's
repositories then starts a PR-triage agent run, and its review comes back as
a pull request comment. Every other account still sees no agent route, field
or event (ADR-20261027090010 §1, ADR-20261023115741).

Dashboard: **faas agent runs (ADR-20261027090010)**
(`deploy/grafana/agent-runs.json`). There are no agent alerts yet, so watch
the dashboard while the dogfood runs.

Every switch below is off by default. The per-daemon switches are
environment variables with no manifest key. Set them with a systemd drop-in,
which release rollouts leave alone; they re-render `/etc/faas/*.toml` from the
manifest. Name each drop-in `60-agents.conf`, so backing out is removing one
file per daemon.

## Before you start

- **Release.** The data lane has applied the 13 agent migrations
  (`20261029093503390` … `20261029093627468`), and every daemon below runs a
  release that contains #5730 (`593c954510`) and the agentd run metrics.
- **Account.** You have the account's UUID, an API token for it, and the
  Gregale GitHub App is installed on the repository you will open the test
  pull request on.
- **Model key.** You have an Anthropic API key that belongs to the account.
  It becomes the account's own AI integration; it is never a platform key.
- **Agent-pool node.** You picked one compute node with room for the runs:
  1 GiB per concurrent run with the presets' default size, and a Pro account
  runs 5 at once (`AgentRunConcurrentPerAccount`). Use its compute node ID or
  name below.
- **Relay.** Agent runs reach the model only through vmmd's outbound relay and
  outboundd, the same path apps' outbound integrations use. Check that the
  node's vmmd has `FAAS_VMMD_OUTBOUND_RELAY_TARGET` and a workload-identity
  signing key (`FAAS_WORKLOAD_IDENTITY_KEY_PATH`), and that outboundd's
  `/etc/faas/outboundd.toml` sets `relay_listen` and
  `workload_identity_jwks_path`. If any is missing, set the relay up first: a
  run without it fails at its first model call.
- **Agent base.** The images workflow publishes `ghcr.io/poyrazk/agent-base`
  for every main commit as `ci-<sha>`. Resolve the release commit's image to
  its digest:

  ```bash
  docker buildx imagetools inspect ghcr.io/poyrazk/agent-base:ci-<release-sha> --format '{{json .Manifest}}' | jq -r .digest
  ```

## 1. Stage the agent base on the agent-pool node

On the agent-pool node, give imaged the digest-pinned base:

```ini
# /etc/systemd/system/faas-imaged.service.d/60-agents.conf
[Service]
Environment=FAAS_AGENT_BASE_REF=ghcr.io/poyrazk/agent-base@sha256:<digest>
```

```bash
systemctl daemon-reload && systemctl restart faas-imaged
journalctl -u faas-imaged -f   # wait for the agent base and layer to stage
ls -la /srv/fc/base/runner-agent-amd64.ext4* /srv/fc/base/agent-layer-v1-amd64.ext4
```

imaged scans the base with Grype before it publishes it; vmmd boots only a
base whose scan sidecar admits it. Leave `FAAS_AGENT_BASE_REF` unset on every
other node, so they hold no agent residency.

## 2. vmmd on the agent-pool node

```ini
# /etc/systemd/system/faas-vmmd.service.d/60-agents.conf
[Service]
Environment=FAAS_VMMD_AGENT_RUNS=1
```

Restart vmmd the way a release does. Without this switch vmmd refuses
`RunAgent` with Unimplemented, which agentd counts as a requeue.

## 3. schedd on the control plane

```ini
# /etc/systemd/system/faas-schedd.service.d/60-agents.conf
[Service]
Environment=FAAS_SCHEDD_AGENT_RUNS=1
Environment=FAAS_SCHEDD_AGENT_RUN_NODES=<agent-pool node ID or name>
Environment=FAAS_SCHEDD_AGENT_STEPS=1
```

```bash
systemctl daemon-reload && systemctl restart faas-schedd
```

Leave `FAAS_SCHEDD_AGENT_RUN_SNAPSHOTS` (preset snapshots) and
`FAAS_SCHEDD_AGENT_TRIGGERS` (broker trigger targets) off for the first pass:
every run cold-boots, and only the workflow agent step admits runs.

## 4. outboundd and githubd on the control plane

Add `agent_runs_enabled = true` to `/etc/faas/outboundd.toml`. Releases
install that file only when it is absent, so the edit persists. If the file
sets `ai_gateway_accounts`, the account must be in it. Restart outboundd.

githubd forwards pull request deliveries to apid as `gregale.github` events.
apid publishes them only for accounts the agents capability is released to:

```ini
# /etc/systemd/system/faas-githubd.service.d/60-agents.conf
[Service]
Environment=FAAS_GITHUBD_AUTOMATION_EVENTS=1
```

githubd also needs `FAAS_APID_GITHUBD_BRIDGE_SOCK`, which the release sets
for preview environments. Restart githubd.

## 5. agentd on the control plane

agentd is the only writer of agent runs after apid queues them. Converge it
with the `agentd_service` role:

```yaml
# control-plane host_vars
faas_agentd_managed: true
faas_agentd_env_src: /path/on/the/controller/agentd.env   # holds DATABASE_URL only
faas_agentd_concurrency: 4
```

The role's defaults turn on checkouts, output files and deliveries, and serve
`/metrics` on `127.0.0.1:9109`; Prometheus scrapes it under the `agentd` job
once `faas_agentd_managed` is true. Check it:

```bash
systemctl status faas-agentd
curl -fsS http://127.0.0.1:9109/metrics | grep -E '^agent_run'
```

The queue gauges read 0 until the first lease sweep, ten seconds after
start.

## 6. Release the agents capability to the account

```bash
gregalectl flags allow --key agents --account-id <account-uuid> --reason "agents dogfood" --yes
gregalectl flags inspect --key agents --account-id <account-uuid>
```

Only this account now sees `/v1/agents` and `/v1/agent-runs`, can save
agent steps, and gets `gregale.github` events.

## 7. Create the agent and its automation

As the account, with `API` the production API origin and `TOKEN` its API
token. First the account's Anthropic integration, with its key read from the
environment so it stays out of shell history and URLs:

```bash
integration=$(curl -fsS -X POST "$API/v1/outbound/integrations" -H "Authorization: Bearer $TOKEN" \
  -H 'content-type: application/json' \
  -d '{"name":"anthropic","origin":"https://api.anthropic.com","allowed_methods":["POST"],
       "allowed_path_prefixes":["/v1"],"ai_provider":"anthropic"}' | jq -r .id)
jq -n --arg key "$ANTHROPIC_API_KEY" '{authorization: $key}' |
  curl -fsS -X PUT "$API/v1/outbound/integrations/$integration/credential" -H "Authorization: Bearer $TOKEN" \
    -H 'content-type: application/json' -d @-
curl -fsS -X PUT "$API/v1/outbound/integrations/$integration/ai-prices" -H "Authorization: Bearer $TOKEN" \
  -H 'content-type: application/json' \
  -d '{"entries":[{"model_prefix":"claude-opus","input_millicents_per_mtok":1500000,"output_millicents_per_mtok":7500000},
                  {"model_prefix":"claude-","input_millicents_per_mtok":300000,"output_millicents_per_mtok":1500000}]}'
```

Then the agent. The run's checkout is the head commit's tree, with no history
and no diff against the base, so the prompt asks for a triage of what the
title describes. The `github_comment` delivery posts `result.json`'s
`comment` field. The spend cap is $2 per run:

```bash
jq -n --arg integration "$integration" '{
  name: "pr-triage",
  harness: {preset: "claude-code"},
  prompt_template: "You are triaging pull request #{{inputs.number}} in {{inputs.repository}}, titled \"{{inputs.title}}\". The current directory is the head commit of the pull request. Find the code the title is about and write output/result.json with one key, \"comment\": a short Markdown review with a one-sentence summary, a risk (low, medium or high) and at most three suggested labels.",
  timeout_sec: 900,
  integrations: {anthropic: $integration},
  ai_budget_millicents: 200000,
  deliveries: [{type: "github_comment", field: "comment"}]
}' | curl -fsS -X POST "$API/v1/agents" -H "Authorization: Bearer $TOKEN" -H 'content-type: application/json' -d @-
```

The automation is a workflow in an app's `gregale.yaml`. Deploy any small app
of the account with this workflow:

```yaml
schema_version: 1
workflows:
  - name: pr_triage
    trigger:
      type: event
      source: gregale.github
      event_type: pull_request.opened
      filter:
        data:
          pull_request:
            fork: false
    steps:
      - name: review
        agent: pr-triage
        input:
          inputs:
            repository: "{{input.data.repository.full_name}}"
            number: "{{input.data.pull_request.number}}"
            title: "{{input.data.pull_request.title}}"
          checkout:
            repository: "{{input.data.repository.full_name}}"
            commit: "{{input.data.pull_request.head.sha}}"
          github:
            repository: "{{input.data.repository.full_name}}"
            number: "{{input.data.pull_request.number}}"
```

Fork pull requests are filtered out: their content is untrusted and they run
nothing.

## 8. Open a pull request and watch it

Open a pull request on the repository. On the dashboard, expect:

1. `Queued runs` rises to 1, and `queue` in **Start stages p95** stays under a
   few seconds.
2. `place`, then `boot`: a cold boot of the agent base, seconds rather than
   milliseconds on this first pass.
3. **Runs finished by outcome** shows `succeeded`, and **Deliveries ended**
   shows `github_comment delivered`. The comment appears on the pull request.

Read the run itself:

```bash
curl -fsS "$API/v1/agent-runs?limit=5" -H "Authorization: Bearer $TOKEN" | jq '.items[] | {id, state, outcome, finished_at}'
curl -fsS "$API/v1/agent-runs/<run-id>/outputs" -H "Authorization: Bearer $TOKEN" | jq .   # result, files and log tail
```

## When it does not work

| What you see | Where to look |
|---|---|
| `Queued runs` grows and nothing is claimed | `systemctl status faas-agentd`; agentd's log; the account is at its concurrent-run limit |
| `Requeues by cause`: `Unimplemented` | schedd or vmmd is missing its switch (steps 2–3) |
| `Requeues by cause`: `ResourceExhausted` | the agent-pool node has no room; check `FAAS_SCHEDD_AGENT_RUN_NODES` names it |
| `Requeues by cause`: `Unavailable` | schedd or vmmd unreachable; their logs |
| No run at all after the pull request | `gregalectl flags inspect --key agents`; githubd's log for the delivery; `FAAS_GITHUBD_AUTOMATION_EVENTS`; the workflow's filter |
| Run `failed` with `checkout_failed` | agentd's githubd target and storage settings; the App's access to the repository |
| Run `failed` or `timed_out` at the first model call | the relay prerequisites; outboundd `agent_runs_enabled`; the integration's credential |
| Run `succeeded`, no comment | `Delivery attempts` by result; agentd's log for the delivery; `result.json` has no `comment` |

Outcome codes are on the run (`outcome.code`), never metric labels; a run's
log tail is in its outputs.

## Turning it off

Work back from the edge, so nothing new starts while runs drain:

```bash
gregalectl flags revoke --key agents --account-id <account-uuid> --reason "agents dogfood over" --yes
```

Then remove the `60-agents.conf` drop-ins from githubd, schedd, vmmd and imaged
and restart those daemons; remove `agent_runs_enabled` from outboundd's TOML;
and converge with `faas_agentd_enabled: false`, which stops agentd and removes
its configuration. Runs already queued stay queued and invisible until the
capability returns. The agent base and layer files on the node are cache: an
unset `FAAS_AGENT_BASE_REF` stops imaged from staging them again.

## Next passes

- Preset snapshots: `FAAS_SCHEDD_AGENT_RUN_SNAPSHOTS=1` on schedd. A shape's
  first run cold-boots and vmmd captures its snapshot; later runs restore from
  it, and `boot` on the dashboard drops.
- Email deliveries: `FAAS_INTERNAL_MAIL_AGENT_RUN_DELIVERED_OUTBOX_ENABLED=1`
  on apid and maild, on top of the producer outbox gates.
- Broker trigger targets: `FAAS_SCHEDD_AGENT_TRIGGERS=1` on schedd.
