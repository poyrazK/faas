# ADR-057 — Runtime healthcheck probe (`pkg/fcvm/vmm.go::waitReady`)

- **Status:** proposed
- **Date:** 2026-07-31
- **Issue:** #460 (filed under ADR-053, §Decision 6 deferred)
- **Decision:** When `AppManifest.Healthz != ""`, `vmm.waitReady` does an HTTP
  `GET <Healthz>` against `<HostIP>:8080`; `2xx` = ready, non-`2xx` / err =
  retry until `readyTimeout`. Empty `Healthz` keeps the legacy TCP-accept
  on `:8080` byte-identical. Timing parameters (`IntervalS`, `TimeoutS`,
  `Retries`) ship in a follow-up ADR.

## Why

ADR-053 §Decision 6 settled the persistence half of the `healthcheck`
override (column `deployments.override_healthcheck jsonb`, DTO
`DeploymentHealthcheck`, manifest stamp `manifest.Healthz`) but
explicitly deferred the runtime half. The host's
`pkg/fcvm/vmm.go::waitReady` lingers on a bare TCP accept that probes
the wrong invariant: `:8080` accepting means the guest process bound
*something*, not that the customer's app is ready to serve real
traffic. A customer who runs `npm start` followed by a 5-second
`connectDB().then(...)` block today answers the load-balancer's
probe with a 500-class error during the cold-boot window, which the
gateway treats as a wake failure and the customer's users see as a
503 cold-boot retry.

Per ADR-009 (`identical inner network world`) + the portnorm ladder
(`guest/init/portnorm_linux.go`), the host's `:8080` already reaches
the customer's app bind port inside the guest — the bridge re-exposes
the override port on `:8080` so the host DNAT stays a single key.
That means the healthcheck path is the *only* knob the customer can
configure today: the port is the host's choice (ADR-009), the path is
the customer's choice.

The "why now" is issue #460 itself: 5 of 6 override fields are
runtime-effective (entrypoint / cmd / env / env_secrets / port). The
sixth (`healthcheck`) is dormant on `manifest.Healthz` and is the
remaining gap. Closing it lands issue #460 entirely.

## Decision

**1. Probe site: `pkg/fcvm/vmm.go::waitReady`, NOT guest-init.**
The host probe matches the existing TCP-accept site (single
file-edits to branch on `HealthcheckPath`), keeps the legacy
"empty = TCP `:8080`" default byte-identical, and avoids running
customer probe code inside the guest's portnorm ladder. Routing
the probe through guest-init would require re-keying the inner
netns and a vsock verb (the route the characterization report
takes, ADR-051); the HTTP probe is cheaper and the path is a
trivial string.

**2. Probe target: `<HostIP>:8080`, NOT the customer's `EffectivePort()`.**
ADR-009 + portnorm already re-exposes the customer's bind port on
`:8080` inside the guest; the host DNAT stays
`:8080 → 10.0.0.2:8080`. Probing the override port directly would
require a second readiness DNAT key per-app, which contradicts
ADR-009 and adds netns complexity. The path is the customer's
choice; the port is the host's choice.

**3. Probe shape: `GET <Healthz>`, accept `2xx` only.**
- `HEAD` rejected: a 404 with a body that happens to parse as
  `200 OK` on a HEAD would silently pass a probe that should have
  failed. `GET` is unambiguous.
- Status code allowlist rejected: the path is the customer's
  choice; the customer picks the success criteria. Document the
  `2xx` rule as the v1 contract; an ADR follow-up can widen to
  e.g. `3xx follow + 4xx fast-fail` if customers ask.
- `Content-Type` not inspected: the probe is "alive enough to
  answer", not "shape-conformant". Per-deployment strict content
  checks are out of scope (PR-C mirror: latency-only probe).

**4. Failure semantics: non-`2xx` = retry until `readyTimeout`.**
No per-probe fast-fail. This matches PR-C's "wake must always
work" stance (ADR-005): a transient customer-app 500 must not
wedge a wake. The loop sleeps 200ms between probes (matches the
existing TCP probe cadence). The HTTP client itself has a 2s
timeout per probe (bounded — the deadline is `readyTimeout`,
default 30s). On `ctx.Done()`, the function returns the ctx
error; on deadline elapse, the function returns `ErrReadyTimeout`.

**5. Wire field: `vmmd.proto::AppSpec.healthcheck_path = 11` (string).**
Additive per ADR-016. Empty string = legacy TCP-accept probe
(matches PR-C's `port = 0` default stance). The
`pkg/sched/vmmclient.go::AppSpec.HealthcheckPath` and
`pkg/fcvm/manager.go::WakeRequest.HealthcheckPath` mirror this
exactly. `pkg/fcvm/manager.go::Instance.HealthcheckPath` is
stamped at Wake time so server-side readers can resolve without
a second request lookup (mirror PR-C's `Instance.Port`).

**6. plumb-through: `ColdBootSpec.HealthcheckPath` /
`RestoreSpec.HealthcheckPath`.**
Both `pkg/fcvm.ColdBootSpec` and `pkg/fcvm.RestoreSpec` gain a
single `HealthcheckPath string` field. `bringUp` propagates
`req.HealthcheckPath` into the spec. `JailerVMM.waitReady`'s
signature changes from `(ctx, l)` to `(ctx, l, healthcheckPath)`
so the call sites (Boot line 305, Restore line 464) pass the
spec value through. The `Lease` struct stays allocator-only —
no need to denormalise the path onto the lease; the spec is
already in scope at the call site.

**7. Schedd: `healthcheckPathFromDep(dep)` helper.**
Mirror of `envSecretsFromDep` (pkg/sched/engine.go:1647). Reads
`dep.OverrideHealthcheck` (jsonb), decodes the path into a
`string`, returns `""` for nil / malformed. Aimed at the spec
literal at engine.go:743 (Wake) and engine.go:1183 (Prime).
Bad data is treated as no-override (not fail-wake), defensive
mirror of `envSecretsFromDep`: the apid validator already
enforces the shape at INSERT time, so a malformed column is a
contract violation that surfaces as a missing healthcheck
(legacy TCP probe) rather than a wake error.

**8. Out of scope (explicit deferrals).**
- `IntervalS` / `TimeoutS` / `Retries` — the column already
  stores them (`Deployment.OverrideHealthcheck jsonb`), the DTO
  validates them (`pkg/api/dto.go:298-318`), but the vmmd wire
  does not carry them. A v2 contract ADR lands them when a
  customer asks for sub-200ms probe cadence or aggressive
  fast-fail.
- `HEAD` vs `GET` — `GET` is the v1 contract.
- Per-probe fast-fail — wake must always work (ADR-005).
- Per-deployment healthcheck metrics labels — observability
  follow-up ADR; the `gateway_request_duration_seconds` series
  already labels `{app, class}` (ADR-051 §Decision 7).
- Live mutation of `Healthz` on an already-running instance —
  redeploy produces a new lifecycle (existing behaviour).
- Healthcheck path validation beyond "starts with `/`" — DTO
  rule already exists.
- Guest-init probe (ADR-051 characterization-style) — host
  probe is cheaper and the path is the customer's choice.

## gRPC readiness extension (PR #3502)

Deployments may select the standard `grpc.health.v1.Health/Check` RPC
instead of the HTTP or legacy TCP readiness probe. The host dials the
same `<HostIP>:8080` endpoint. An empty service name checks overall
server health; a non-empty `healthcheck_grpc_service` checks that named
service. Only `SERVING` passes readiness. `NOT_SERVING`, unknown
services, transport errors, and other non-serving responses are retried
until the existing startup deadline expires. When gRPC readiness is not
selected, the HTTP and legacy TCP behavior above is unchanged.

## Failure modes

| Scenario | Behaviour |
|---|---|
| Path validation fails at deploy (e.g. empty string) | DTO rejects (400, `CodeValidation`). Pre-validated upstream. |
| App boots, `/healthz` returns 200 within 30s | `waitReady` returns nil. Wake succeeds. |
| App boots, `/healthz` returns 500 for 3s then 200 | `waitReady` retries at 200ms cadence, returns nil after 3s. Wake succeeds. |
| App never starts, `/healthz` 500 indefinitely | `waitReady` returns `ErrReadyTimeout` after 30s. Wake fails. |
| App crashes mid-wake (process gone) | `waitReady`'s dial fails closed, returns `ErrReadyTimeout`. |
| Customer app exposes `/healthz` only on a non-8080 port | PR-C port ladder re-exposes the customer bind on `:8080`; the healthcheck path is *not* bound to the override port. Customer configures `EffectivePort()` and the portnorm ladder does the rest. |
| Customer's reverse proxy strips `/healthz` | Documented v1 contract: the path is the customer's choice, including reverse-proxy posture. Probe fails closed (= no wake). |
| `Healthz` is empty (legacy path) | Existing TCP accept on `:8080`, byte-identical behaviour. Zero regression risk. |
| Two deployments of the same app race an `Healthz` change | `realizeInstance` is per-deployment; the apps row carries `Healthz` per-deploy via imaged's stamp, not per-app. No race. |
| Wire field decoded as garbage (e.g. `\\x00`) | Pass-through to `waitReady`'s `client.Get("http://" + addr + path)` — `net/http` rejects control characters. Treated as probe failure → retry → `ErrReadyTimeout`. |
| Probe-style cross-VM contamination | Impossibility: the host DNAT + per-netns isolation (ADR-009) means the only listener the host can reach inside the per-instance netns is the customer app. No cross-app probe leak. |

## Security

- **Probe site is loopback inside the per-instance netns.** The
  host probe reaches the guest via the same DNAT the forwarder
  uses (`pkg/netns/config.go` MASQUERADE + DNAT `:8080 → 10.0.0.2:8080`).
  The customer app's bind port is re-exposed on `:8080` by
  portnorm, so the probe path is the only knob the customer can
  configure; the probe port is the host's choice.
- **Cross-VM probe contamination impossible.** ADR-009 + the
  per-netns topology means the host's `HostIP` for one instance
  is unreachable from another's netns. The probe target is the
  per-instance `Lease.HostIP`, not a shared address.
- **Probe is unauthenticated.** Mirrors the legacy TCP accept
  (no auth). The probe path is the customer's choice; the
  customer configures their reverse-proxy posture. A customer
  who wants confidential healthcheck logic runs the probe inside
  an auth-bypass branch on the app (e.g. `req.ip === localhost`).
- **No secret material on the wire.** The probe sends `GET
  <path>` with no body, no headers, no body. The
  `HealthcheckPath` field carries only the path string. The
  host probe never logs the path (the path is non-sensitive).
- **DDoS surface.** The probe is bounded by `readyTimeout` (30s
  default) and the 200ms backoff; a malicious app that hangs
  `/healthz` can't keep the host spinning past the deadline.
  After deadline → `ErrReadyTimeout` → wake fails closed (no
  infinite retry).
- **§11 sign-off.** The probe is a host-side TCP/HTTP read; the
  cgroup scope is unchanged, the jailer uid is unchanged, the
  netns is unchanged, the seccomp filter is unchanged. No
  section-11 surface widens.

## Consequences

- **`api/proto/onebox/faas/vmmd/v1/vmmd.proto::AppSpec`** gains
  `string healthcheck_path = 11;`. Additive per ADR-016.
  `make proto` regenerates `vmmdpb.AppSpec.HealthcheckPath`.
- **`pkg/sched/vmmclient.go::AppSpec`** gains `HealthcheckPath string`.
  `toProto()` sets `HealthcheckPath: a.HealthcheckPath` on the
  wire.
- **`pkg/vmmdgrpc/proto.go::toWakeRequest`** + `toColdBootRequest`
  populate `fcvm.WakeRequest.HealthcheckPath` from
  `app.GetHealthcheckPath()`.
- **`pkg/fcvm/manager.go::WakeRequest` / `Instance` /
  `ColdBootRequest`** gain `HealthcheckPath string`. `Wake`
  stamps `HealthcheckPath` onto the live `Instance` (mirror PR-C
  `Instance.Port`).
- **`pkg/fcvm.ColdBootSpec` / `RestoreSpec`** gain
  `HealthcheckPath string`. `bringUp` propagates from
  `req.HealthcheckPath`. `JailerVMM.BootColdBoot` and
  `JailerVMM.Restore` set the spec value before calling
  `waitReady`.
- **`pkg/fcvm.JailerVMM.waitReady`** signature changes from
  `(ctx, l)` to `(ctx, l, healthcheckPath)`. The legacy
  TCP-accept loop is the `healthcheckPath == ""` branch; the
  HTTP `GET` loop is the non-empty branch.
- **`pkg/sched/engine.go`** gains `healthcheckPathFromDep(dep)
  string` helper (mirror of `envSecretsFromDep`). The Wake spec
  literal at line 743 and the Prime spec literal at line 1183
  set `HealthcheckPath: healthcheckPathFromDep(dep)`.
- **Tests.** `pkg/fcvm/manager_test.go::TestColdBootSuccessStampsInstanceHealthcheckPath`
  mirrors `TestColdBootSuccessStampsInstancePort` (PR-C).
  `pkg/vmmdgrpc/proto_test.go::TestToWakeRequest_ForwardsHealthcheckPath`
  + `TestToColdBootRequest_ForwardsHealthcheckPath` mirror the
  PR-C port tests. `pkg/fcvm/vmm_test.go` gains a new
  `waitReady` HTTP probe test (httptest.Server, 200 / 500 /
  timeout) plus a `waitReady` legacy TCP test (verifies the
  `""` branch stays byte-identical).
- **E2E.** `cmd/e2e/deploy_override_healthcheck_metal_test.go`
  gains a Node 22 fixture that registers `/healthz` (200) and
  binds `:8080` (PR-C legacy port — the healthcheck uses
  portnorm, not override-port). The test publishes the
  override, walks the wire end-to-end, and asserts the guest
  responds through the gateway.
- **No new SDK method.** `CreateDeployment` already accepts the
  override shape (PR-A / PR-B). The new wire field is internal
  to vmmd grpc — no SDK surface widens.
- **No `pkg/api/limits.go` change.** The path is bounded by the
  DTO's "starts with `/`" rule (already present in PR-A). No
  quota relax/widen is needed.
- **No migration.** Column already on main
  (`migrations/00079_deployment_overrides.sql`).
- **No new metric.** vmmd's `waitReady` outcome is not exported
  to Prometheus in the legacy path; PR-D mirrors that. A
  follow-up observability ADR can add `vmmd_healthcheck_probe_seconds`
  if a customer asks for it.

## Rejected alternatives

- **Probe through guest-init via vsock (mirror of ADR-051
  characterization).** Adds a new vsock verb + a guest-init
  HTTP client + a per-instance ACK. The host probe is one
  short Go file; the guest-init path is at least four
  (vsock listener, HTTP client, JSON envelope, error handling).
  No customer-visible benefit — the path is the same string either
  way.
- **Probe via `http://<HostIP>:AppPort` aka the customer port.**
  Requires per-instance DNAT keying for the override port (not
  the host `:8080`). Contradicts ADR-009's identical-inner-world
  constraint and adds netns complexity to the readiness path.
- **`HEAD` instead of `GET`.** Some HTTP servers return `200` on
  `HEAD` even when the underlying handler would return 500 — the
  probe would silently pass a wake that should have failed. `GET` is
  unambiguous; the per-probe byte cost is negligible (the
  customer app is the listener, not the host).
- **Status-code allowlist per deployment.** Lets a customer
  declare "200, 204 = ready". Useful for shape-strict apps, but
  the v1 contract is "you own success semantics via the path";
  widening if customers ask is a follow-up ADR.
- **Carry `IntervalS` / `TimeoutS` / `Retries` in v1.** The wire
  field is a single string; carrying the three timing knobs
  requires additional proto fields + a vmmd-side scheduler. The
  v1 default (200ms backoff, 2s per-probe, 30s deadline) is the
  same default the legacy TCP probe used. A customer who wants
  sub-200ms cadence is a follow-up ADR.
- **Async probe (fire-and-forget, mark RUNNING on socket bind).**
  Trades correctness for latency. A customer whose healthcheck
  is "Have I connected to Postgres and pre-warmed a cache?"
  needs the probe to gate RUNNING. Async probe would re-introduce
  the 500-during-cold-boot failure mode this PR closes.
- **Make `Healthz` vmmd's wire-level default (no longer optional).**
  Legacy apps without a healthcheck still need a wake path
  (the TCP accept is the implicit "I bound :8080" probe). Making
  the field required would break every pre-460 deploy in
  production.
- **Probe inside the customer container with a guest-init SDK.**
  Requires every runtime image to include the SDK. The host
  probe has zero customer-visible footprint — it's a host-side
  HTTP GET against the existing DNAT target.

## Downstream

- **Issue #460 closure.** After PR-D ships, every field on
  `CreateDeploymentOverrides` produces runtime effect at the
  host's first wake. The issue can be closed.
- **PR-D follow-ups (out of scope here):**
  - ADR-058 (proposed): v2 contract for `IntervalS` /
    `TimeoutS` / `Retries` wire fields + a vmmd-side probe
    scheduler.
  - ADR-059 (proposed): healthcheck observability — a
    Prometheus counter for `vmmd_healthcheck_probe_total{result}`
    where `result ∈ {2xx, non_2xx, timeout, ctx_cancel}`.
  - ADR-060 (proposed): per-deployment healthcheck metrics
    labels — bounded cardinality argument
    (ADR-053 §Decision 7's deferred observability question).
