# ADR-20261027090000 · A raw-TCP routed port uses TCP readiness

- **Status:** accepted
- **Date:** 2026-10-27
- **Amends:** the source-build readiness default (spec §6.4 amendment 1, #2469), which probes the framework profile's `/healthz` over HTTP
- **Decision:** when a deployment routes to a port the app declares as a public TCP listener (`ports: [{port: N, protocol: tcp}]`, not `internal`), and nothing declares a health path, startup readiness is TCP accept on that port instead of `GET /healthz`.
  - Any declared path keeps HTTP readiness: a deploy override, an environment-scoped `healthcheck` or `healthz`, gregale.yaml `hosting.health`, or a path other than the platform default (for example `/health` found in the source).
  - gregale.yaml's declaration is recorded on the framework profile as `health_from_config`, because its value can equal the default.
  - Direct OCI images already use TCP readiness without an override; they are unchanged.
- **Why:** a source-built TCP service could never become ready.
  - Every source build carries `/healthz` as its default readiness path, and gregale.yaml cannot turn it off.
  - A raw TCP server (a database, an echo service, a game server) answers the HTTP GET with its own protocol, so no probe ever got a 2xx.
  - Container qualification on 2026-10-26: `TestTCPIngressMetal`'s Node TCP echo listened on :5432 two seconds into boot, and the prime failed 30 s later with "readiness probe GET /healthz on :5432 got ECONNREFUSED … the app never listened".
  - The message was also wrong. Only the probes before the bind were refused; the later non-HTTP replies were not counted.

## Details

- **Who decides.** schedd resolves the probe (`healthcheckPathForApp`), from the app's declared ports and the deployment's routed port. vmmd and the guest are unchanged.
- **vmmd's message.** An HTTP probe whose connection the app accepts and answers without HTTP is now counted. When any were seen, the failure is `app_startup_timeout` at `handler_healthcheck`, naming the port and pointing at the TCP-listener declaration, instead of `app_not_listening`.
- **HTTP apps that declare their main port.** An HTTP app that lists its routed port as a TCP listener and declares no health path gets TCP readiness. That is the same contract a direct OCI image has. It can restore HTTP readiness with `hosting.health` or a deploy override.

## Consequences

- Source-built raw TCP services deploy and wake. `TestTCPIngressMetal` exercises the path.
- Deployments built before this change have no `health_from_config` key. If such a deployment declared `hosting.health: /healthz` and routes to a declared TCP listener, it gets TCP readiness until it is redeployed.
- The new profile key is `omitempty`; older readers ignore it.
