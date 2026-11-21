# ADR-847: Opt-in synthetic probes for routes without organic traffic

- Status: Accepted (amended 2026-10-29, 2026-11-09, 2026-11-19)
- Date: 2026-10-09
- Related: ADR-454, ADR-459, ADR-482, ADR-843, ADR-846
- Amends: ADR-454 (observed evidence only)

## Context

ADR-846 pools sparse organic traffic over the stage, but a critical route that
receives no traffic during a canary still has no evidence: it is never judged
in report mode and holds an enforced rollout. The hosting smoke (ADR-482/843)
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

## Amendment 1 (2026-10-29): probes are on by default, through the local gateway

Production never set `FAAS_ROUTE_PROBE_URL`: `route_probe_observations` held no
rows, so the ADR-1446 default probe never ran. Every quiet safe release waited
`CanaryLowTrafficMaxHold` at each stage (rc.304's release acceptance redeploy
spent 10 of its 16 rollout minutes in two such holds), customers' releases
included.

- The control-plane role now enables probes by default (`route_probes_enabled:
  true`); setting it false turns them off. The billing consequence above is the
  one ADR-1446 already accepted for the default probe.
- apid sends probes to the control plane's own `gatewayd-public` listener
  (`faas_route_probe_url`, default `http://127.0.0.1:8080`), the public
  edge's upstream, rather than to the CDN-fronted apps domain. The metal
  tests exercise this path; the CDN path (TLS name `gregale.dev`, `Host` an
  app subdomain) was never tested. Exact-deployment routing, the challenge
  tokens and the response proof are unchanged, and a gateway that has not
  received a challenge still refuses the probe, which then does not count.

## Amendment 2 (2026-11-09): probes pass the platform's auth gates

In production, every probe to an app with `require_authn` or a bearer
`public_auth` mode (33 of 91 apps) was refused 401 by the gateway before the
deployment answered. The proof header is set only on an answered response, so
none counted: such a canary sent about 260 probes a stage, recorded no
evidence, and advanced after `CanaryLowTrafficMaxHold` on no samples (all nine
protected-app rollouts in two weeks).

- A validated probe (a live challenge for this app and deployment, GET or HEAD)
  passes the platform's own gates, `require_authn` and `public_auth`, as the
  deployment smoke challenge already does. It carries no customer credential,
  and every auth check the app performs itself still applies; those routes
  answer 401/403 and count as unauthenticated probe responses.
- The probe paths are `/` or the app's `release_probe_paths` (ADR-1449). An
  app whose GET handler behind its platform gate has side effects should name
  a safe probe path.

This replaces "Probes reach only routes the customer's own auth gates allow
anonymously" for the platform's gates.


## Amendment 3 (2026-11-19): probes do not reset the idle timer

The gateway kept route probes out of request telemetry and usage, but a
probe's 2xx still reset the instance's idle timer. In production three apps
whose canaries had been aborted on 2026-11-15 kept two live deployments at
their split weights, were probed every minute, and so never went idle: ten
1 GiB instances stayed resident and billed for four days, and one app
(`e2edp-1115-r`) never parked again, captured no snapshot, and cold-booted
every one of its 184 scale-up instances and 169 request wakes
(`cold_reason=no_snapshot`).

- A route probe no longer resets the idle timer. It still refreshes the
  gateway's own cached target, since a 2xx proves the target alive.
- This replaces "Probes … keep the canary warm" above. During a canary a quiet
  deployment parks after its idle timeout like any other, and the next probe
  wakes it again: a restore from the snapshot the park kept, billed like any
  request-triggered wake. Probe evidence is unchanged; a probe that wakes the
  deployment counts like any other.
- The protection-baseline health probe's park-after-probe rule
  (ADR-20261116120000 §3) is unchanged: an instance only that probe woke still
  parks once the probe is done.
- Separately, probe targets now require the canary's rollout to be pending or
  rolling out, as "only while a canary is in flight" above already said; an
  aborted or completed rollout is no longer probed.
