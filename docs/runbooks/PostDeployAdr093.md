# PostDeployAdr093

**Severity:** info
**Component:** platform-ops
**Family:** route_metrics
**ADR:** ADR-093

Operator-side smoke that PR #856 (ADR-093 deploy rollout) landed every artifact
on the control-plane node, and that the rules/services/templates wired up the
way the PR intended. Run this in the hour after `ansible-playbook` finishes
and before flipping the deploy dashboard green.

The smoke is **read-only**: no daemons are restarted, no configs are mutated,
no customer state is touched. Every check is a `stat`, `grep`, or `curl` that
an operator can run cold without context.

## Pre-flight

```sh
# Confirm we're on the right box. Today's production is faas-fsn-1 — the
# historical Hetzner EX44 is no longer the target. See host_vars/faas-fsn-1.yml
# and docs/STATUS.md:14.
hostname -f                      # → faas-fsn-1 (or whatever host_vars/ declares)
hostname                         # sanity-check uname too

# Confirm the deploy SHA matches git HEAD on the box. The SHA the operator
# noted at deploy time should match `git rev-parse HEAD` on the control-plane
# node's checkout of the faas repo.
cd /srv/faas && git rev-parse HEAD
```

If the host or SHA does not match expectations, **stop** — the deploy went to
the wrong place. Re-run `make deploy` against the intended node before
proceeding.

## Artifact verification

Run each block. A `FAIL` line means the artifact did not land and the operator
must re-run the relevant ansible task (each path maps 1:1 to a task in
`deploy/ansible/roles/`).

### 1. Prometheus rule file — `faas.rules.yml`

Dropped by `deploy/ansible/roles/prometheus/tasks/main.yml:147-154`.

```sh
ls -l /etc/prometheus/faas.rules.yml
# expect: -rw-r--r-- 1 prometheus prometheus ... /etc/prometheus/faas.rules.yml

/usr/local/bin/promtool check rules /etc/prometheus/faas.rules.yml
# expect: SUCCESS — /etc/prometheus/faas.rules.yml

grep -A2 'name: GatewayWildcardRoute' /etc/prometheus/faas.rules.yml
# expect: 12 lines including expr keyed off
# gateway_requests_by_route_total{route="__route_other__"}
```

If `promtool check rules` fails, the file is malformed (PR #856 G8.9 fixed the
`FaasApiAvailabilityLow` ratio; if the failure is the ratio rule, the previous
PR regressed — re-run `make deploy` after `git pull`).

### 2. Prometheus systemd unit — `ExecReload`

Added in PR #856 commit `a9e6d64a` at
`deploy/ansible/roles/prometheus/files/prometheus.service:31`.

```sh
grep ExecReload /etc/systemd/system/prometheus.service
# expect: ExecReload=/bin/kill -HUP $MAINPID

systemctl show prometheus.service -p ExecReload
# expect the same line — confirms systemd saw the new unit file
```

If `ExecReload` is missing, the operator must `cp` the new unit file from the
checkout and `systemctl daemon-reload`. Without `ExecReload`, rule reloads
fail silently and the new `faas.rules.yml` is never picked up until
`systemctl restart prometheus`.

### 3. gatewayd-internal toml — `route_metrics_enabled`

Dropped by `deploy/ansible/roles/gatewayd_internal_service/tasks/main.yml:28-43`.
Rendered from
`deploy/ansible/roles/gatewayd_internal_service/templates/gatewayd.toml.j2:14`.

```sh
ls -l /etc/faas/gatewayd.toml
# expect: -rw-r----- 1 root faas ... /etc/faas/gatewayd.toml

grep '^route_metrics_enabled' /etc/faas/gatewayd.toml
# expect: route_metrics_enabled = true

# The env override is the live-reload escape hatch — confirm it parses too.
# (The env var is read at startup; the smoke only confirms the helper exists
# in the binary. If the systemd unit doesn't set FAAS_GATEWAY_ROUTE_METRICS,
# the toml key is the binding value.)
systemctl show faas-gatewayd-internal.service -p Environment
# expect either nothing or FAAS_GATEWAY_ROUTE_METRICS=true
```

If `route_metrics_enabled` is missing or `false`, the cluster-wide default
landed wrong. Re-run `ansible-playbook deploy/ansible/playbooks/gatewayd-internal.yml`
with `-e gatewayd_route_metrics_enabled=true`.

### 4. Grafana dashboard — `faas-fleet-m8`

Panels id 103 + 104 added in PR #856 commit `375bdf53` at
`deploy/grafana/faas-fleet.json:598-624` and `:625-644`.

```sh
# Grafana on the box serves the dashboard JSON at its API root.
# Operator-side: open the dashboard at
# https://grafana.<box>/d/faas-fleet-m8/faas-fleet
# and visually confirm two new panels are present under the existing
# fleet traffic row:
#   - "Top routes by req/s + error rate" (id 103)
#   - "Top routes by p95 latency" (id 104)

# Headless check (Grafana token required; export FAAS_GRAFANA_TOKEN):
curl -fsS -H "Authorization: Bearer $FAAS_GRAFANA_TOKEN" \
  "http://127.0.0.1:3000/api/dashboards/uid/faas-fleet-m8" \
  | jq '.dashboard.panels[] | select(.id==103 or .id==104) | {id, title}'
# expect: two lines, ids 103 and 104, with the titles above
```

If either panel id is missing, the dashboard file did not land. Re-run the
grafana role (`deploy/ansible/roles/grafana/tasks/main.yml`) — the mirror
under `deploy/ansible/roles/grafana/files/faas-fleet.json` is byte-identical
to `deploy/grafana/faas-fleet.json` (enforced by `make grafana-mirror-check`).

## Live-fire test

Confirm the `GatewayWildcardRoute` rule loaded on Prometheus:

```sh
curl -fsS http://127.0.0.1:9095/api/v1/rules \
  | jq '.data.groups[].rules[] | select(.name=="GatewayWildcardRoute") | {name, state, expr, duration}'
# expect: one line, state="inactive" (no Hobby-tier apps saturating yet),
# expr keyed off gateway_requests_by_route_total{route="__route_other__"},
# duration="10m0s"
```

Triggering the alert in real time requires pushing 200+ reqs at one Hobby
app with `/users/{uuid}` patterns — out of scope for the smoke. The smoke
only confirms the rule loaded and Prometheus knows about it.

If the rule is absent from `/api/v1/rules`, the rule file did not get loaded:
`curl -fsS -X POST http://127.0.0.1:9090/-/reload` triggers Prometheus to
re-evaluate its rule_files config (requires `--web.enable-lifecycle` on the
Prometheus binary). If reload 404s, run `systemctl restart prometheus`.

## End-state snapshot

Capture the post-deploy state into the operator log (Slack `#deploys` or the
deployment record). The fleet-level SLO surface is unchanged from before
PR #856; the only new surface is the per-route series.

```
post-deploy adr-093 ✓
  sha:    <git rev-parse HEAD on the box>
  box:    <hostname -f>
  rule:   faas.rules.yml SUCCESS, GatewayWildcardRoute present
  unit:   prometheus.service ExecReload=/bin/kill -HUP $MAINPID
  toml:   /etc/faas/gatewayd.toml route_metrics_enabled=true
  panels: faas-fleet-m8 ids 103+104 present
  alert:  GatewayWildcardRoute state=inactive
```

If any line is `FAIL`, **stop** — the deploy is not done. Re-run the relevant
ansible task (each path maps 1:1) before flipping the deploy dashboard green.

## Rollback

PR #856 cluster artefacts can each be reverted independently:

| Artifact | Path | Rollback |
|---|---|---|
| Rule file | `/etc/prometheus/faas.rules.yml` | `rm` (pre-PR #844 surface is in the existing fleet rule file; `promtool check config` and `/api/v1/rules` will reflect the absence) |
| Systemd unit | `/etc/systemd/system/prometheus.service` | `cp deploy/ansible/roles/prometheus/files/prometheus.service /etc/systemd/system/` (PR #855 version, no `ExecReload`) + `systemctl daemon-reload` + `systemctl restart prometheus` |
| gatewayd toml | `/etc/faas/gatewayd.toml` | `rm` (the daemon reads `FAAS_GATEWAY_ROUTE_METRICS` env; absence falls through to the gatewayd-internal default which is `true` per PR #856 commit 6). To force **off**: set `route_metrics_enabled = false` in the toml and `systemctl restart faas-gatewayd-internal` |
| Dashboard | `faas-fleet-m8` | `cp deploy/ansible/roles/grafana/files/faas-fleet.json /var/lib/grafana/dashboards/` (PR #855 version, no panels 103/104) |

The per-app `apps.route_metrics_enabled` flag (PR #844, migration 00216) is
**not** affected by any of these rollbacks — only the surface-level observability
on a single control-plane node is. A rolling deploy of new control-plane nodes
without these artifacts will lose the `_by_route` Prometheus series for those
nodes until the artifacts land again.

## Acceptance

- All four artifact verifications return `✓`.
- `/api/v1/rules` lists `GatewayWildcardRoute` with `state=inactive`.
- End-state snapshot is logged to the deploy record.
- No daemons restarted by the smoke (the smoke is read-only).

## Related

- `docs/adr/093-per-route-app-metrics.md`
- `docs/runbooks/GatewayWildcardRoute.md` — operator playbook for the alert the smoke confirms loaded
- `deploy/scripts/adr093-hobby-audit.sh` — Hobby-tier app audit harness (run mid-sprint, not part of the smoke)
- `deploy/ansible/roles/prometheus/tasks/main.yml:147-154` — rule-file drop
- `deploy/ansible/roles/gatewayd_internal_service/tasks/main.yml:28-43` — toml drop
- `deploy/ansible/roles/prometheus/files/prometheus.service:31` — `ExecReload` line
- `deploy/grafana/faas-fleet.json:598-644` — panels 103 + 104
- `pkg/gateway/route_label_set.go:57` — `__route_other__` constant
- `pkg/api/limits.go:2662-2668` — `Plan.RouteMetricsResponseAllowed()`