# ADR-1449 · App-chosen rollout probe routes

- **Status:** accepted (shipped in PR 4872, 2026-10-18)
- **Date:** 2026-10-16
- **Amends:** [ADR-1446](1446-safe-releases-by-default.md) (default probe of `GET /`), [ADR-1321](1321-evidence-paced-default-rollouts.md) (probe cadence)
- **Follows:** [ADR-847](847-synthetic-route-probes.md) route probes
- **Decision:** An app may name up to three paths (`release_probe_paths`) that the default rollout probe checks instead of `GET /`. Each round splits the 20 probes per revision across them. An empty list keeps `GET /`.
- **Why:** The default probe checks only `GET /`. An app whose `/` needs a login answers 401, so its rollouts fall back to the timer and the 5-minute no-news bound. An API whose `/` is a static page proves little about the code that changed. Letting the app name the routes it cares about fixes both without a route health gate.
- **Rejected alternatives:** Requiring an ADR-847 route health gate (it is a separate, heavier feature with organic-traffic windows and latency limits; most apps want only "probe these paths"). Sending configured headers or credentials with probes (probes must stay subject to the app's own auth; secrets in probe config would widen their reach). Probing every listed path 20 times (60 probes per revision per round for three paths, for no gain in the breaker's 20-response floor). Latency checks on probes (probe latency includes wakes and a cold path; ADR-847 already keeps latency on organic traffic).

## Decision

### The field

`release_probe_paths` is part of the app manifest next to `release_policy`. It holds zero to `api.ReleaseProbePathsMax` (3) distinct paths, each concrete and absolute under ADR-847's probe rules: it starts with `/`, is at most `RouteHealthMaxPathBytes`, and has no query, fragment, wildcard, parameter, whitespace or control character. Probes are always `GET`. The rule moves to `api.ValidProbePath`, which ADR-847's `routehealth.ValidateProbe` now uses too.

It is set with `gregale app <slug> --release-probe-paths /healthz,/api/orders` (an empty value clears it), in the API's create and update app requests, and in the manifest file. `GET /v1/apps/{slug}` returns it.

### Probing

The default probe target carries the app's paths. A round sends `ceil(20 / n)` probes per revision to each of the `n` paths, so a round still meets the breaker's request floor. A path an ADR-847 selector already probes gets at least that many. Each path is recorded under its own route, and the breaker sums them per deployment, as it already sums ADR-847 probes. The unauthenticated share (half of a deployment's probes answered 401 or 403) still makes the evidence unusable.

Nothing else changes: probes still feed only the 5xx check, and only while organic traffic is below the request floor.

## Consequences

- An app with a login on `/` gets probe evidence by naming an open health route, so its default rollouts advance on evidence again.
- Probes reach the paths the app named, through its normal routing and auth. A path that is expensive or has side effects on `GET` is the app's choice; the docs say to name cheap, read-only routes.
- Decision summaries no longer name `/`; they say "synthetic probes".

## Rollout

Ship with the field, the CLI flag and the worker change together. Tests cover validation, the split of probes across paths, a gated selector on a named path, and the API round trip.
