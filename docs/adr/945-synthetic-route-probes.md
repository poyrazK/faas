# ADR-945: Opt-in synthetic probes for routes without organic traffic

- Status: Accepted
- Date: 2026-10-09
- Related: ADR-454, ADR-459, ADR-482, ADR-941, ADR-944
- Amends: ADR-454 (observed evidence only)

## Context

ADR-944 pools sparse organic traffic over the stage, but a critical route that
receives no traffic during a canary still has no evidence: it is never judged
in report mode and holds an enforced rollout. The hosting smoke (ADR-482/941)
can reach an exact candidate, but only before promotion, and it deliberately
bypasses customer auth gates, which is not acceptable for live traffic.

## Decision

A GET or HEAD route health selector can opt into a `probe` with a concrete path
that matches the selector's shape (`/users/42` for `/users/{id}`), at most
`RouteHealthProbeMaxRoutes` (5) per app.

**Runner.** An apid worker runs only when `FAAS_ROUTE_PROBE_URL` names the
public origin. Every minute it lists apps with probed selectors and an
in-flight canary, claims one round per app and minute in `route_probe_rounds`
(so replicas never double-probe), and reads the candidate's route health
report. It probes a selector only while its organic evidence is sparse (or
already rests on probes): `RouteHealthProbeRequestsPerMinute` (10) bodyless
requests per route to the candidate and to the stable deployment, with
bounded concurrency and no redirects.

**Targeting.** apid mints a random token per deployment and publishes it on a
new `route_probe_challenge` channel. Gateways keep probe tokens in a store
separate from hosting-smoke tokens, so a probe token can never authorize the
smoke bypass. A validated probe uses the existing exact-deployment routing,
keeps every customer auth gate, is never retried onto a sibling, and receives
the upstream-only response proof (ADR-482) so the prober counts only
responses that came from the probed deployment. Probe headers are stripped
before forwarding. A request carrying probe headers that fail validation is
refused with 503, so a probe that races its challenge is never served as
customer traffic.

**No telemetry or usage.** The gateway writes no `request_telemetry` row and
no usage event for a probe. The prober records its own per-minute results in
`route_probe_observations` (requests, 5xx, 401/403), pruned after 24 hours.
Analytics, customer reach, production monitoring and billing usage therefore
exclude probes by construction. This replaces the earlier idea of a
`synthetic` flag on `request_telemetry`, which would have changed the
telemetry stream and every one of its 30 readers for the same guarantee.

**Evidence.** For a probed selector that one-minute and pooled organic
evidence leave sparse, the route health report fills `synthetic_windows` from
probe rows over the pooled bounds. `Evaluate` adopts their 5xx verdict with
`evidence_window: synthetic` under the unchanged thresholds. A window where at
least half of either side's probes were rejected by auth gates is unknown
(`probe_unauthenticated`). Probes never settle a selected latency check; a
probe-detected regression is still adopted.

## Consequences

- Rarely called GET routes can gate a canary without customer traffic.
- Probes wake scale-to-zero deployments and keep the canary warm; that time is
  billed like any request-triggered wake. Probes are opt-in and capped.
- Probes reach only routes the customer's own auth gates allow anonymously;
  protected routes stay unknown.
- Deployments must set `FAAS_ROUTE_PROBE_URL` for apid; until then probes are
  off and nothing changes.
