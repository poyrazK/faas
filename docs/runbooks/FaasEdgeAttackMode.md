# FaasEdgeAttackMode

Source: `deploy/ansible/roles/prometheus/files/faas.rules.yml`, group
`faas_edge_security` (ADR-20261024082000 amendment 1).
Alerts: `FaasEdgeAttackModeActive` (info), `FaasEdgeAttackModeRefusingClients`
(warn).
Metric: `gateway_edge_attack_mode_total{app,outcome}` (issued, passed,
bypassed, rejected), a subset of `gateway_edge_challenge_total`.
Design: ADR-20261021220456 and its amendment 1.

## Symptom

A customer switched on attack mode for an app: for 15 minutes to 24 hours
every visitor gets the browser challenge. Browser navigations get the page;
requests that are not browser navigations and carry no verified API key or
JWT are refused with 403 `challenge_required`.

- **Active** (info): the app has been in attack mode for 15 minutes. While
  attack mode is preview, each use is reviewed (GA criterion A3).
- **RefusingClients** (warn): more than half the app's requests are refused,
  over one a second, for 15 minutes. Either a script flood is being turned
  away, which is the point, or the app's own API clients are locked out.

## Verify

```promql
sum by (app, outcome) (rate(gateway_edge_attack_mode_total[15m]))
sum by (app) (rate(gateway_edge_challenge_total{outcome="solved"}[15m]))
```

```bash
# As the customer, or with their consent:
gregale edge-rules attack-mode status --app <slug>
gregale edge-rules events --app <slug> --outcome rejected --since 1h
```

## Check

- **Flood turned away**: rejected requests come from few user agents and
  clients, and the app's own traffic (solved pages, bypassed verified
  credentials) carries on. No action.
- **API clients locked out**: rejected requests are the app's normal API
  traffic, from many clients, with the app's usual user agents. Those clients
  need a verified API key or JWT edge rule, or attack mode should end.
- **Visitors not getting through**: solved stays near zero while issued is
  high across apps; see `FaasEdgeChallenge.md`.

## A3 review (preview)

For each use, record in `docs/STATUS.md` under the edge security release
gate: the app, how long attack mode ran, whether it stopped the flood
(request rate to the app before and during), and whether legitimate clients
got through (solved and bypassed against rejected). A3 is met after a real
flood with no lockout of the app's own clients.

## Silence

```bash
amtool silence add --matchers='alertname=FaasEdgeAttackModeRefusingClients' \
  --matchers='app=<app id>' --duration=6h --comment='flood confirmed; attack mode working'
```

## Recover

1. Lockout: tell the customer; they end it with
   `gregale edge-rules attack-mode off --app <slug>` or add a verified
   credential rule for their API clients. It also ends on its own at its
   expiry.
2. Attack mode misbehaving for everyone: withdraw it with
   `gregalectl flags disable --key edge_attack_mode --reason "..." --yes`.
   Active attack modes go inert at once; the app's own challenge rules keep
   working.
