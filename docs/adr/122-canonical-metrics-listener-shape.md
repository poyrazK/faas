# ADR-122 · Canonical metrics-listener shape across all daemons

- **Status:** Proposed
- **Date:** 2026-08-20
- **Issue:** #995 (Edge protection gaps — code review of the
  platform's envelope protection surface) post-merge audit.
- **Decision:** name the canonical metrics-listener shape that
  every platform daemon's loopback `/metrics` HTTP listener must
  install, and the webhook variant for githubd. New constants in
  `pkg/api/limits.go` (`Metrics*SecondsDefault`,
  `Webhook*SecondsDefault`) are the single source of truth; per-
  daemon `*MetricsListener` helper methods and TOML fields apply
  them.

## Context

PR #996 (`feat(gateway-security): issue #995 edge protection —
full envelope hardening`, merged 2026-08-20 at `76f1180b9`) closed
the four transport-layer envelope gaps on the customer-facing
surfaces: apid control plane, gatewayd-public TLS edge,
gatewayd-internal public listener, and the buffered/streaming
reverse-proxy paths through `pkg/gateway/handler.go`. Every
listener that ships in that PR sets the full set of stdlib
`http.Server` knobs:

- `ReadHeaderTimeout` — pre-existing 10s (Slowloris guard).
- `ReadTimeout` — body cap, prevents runaway scrapers holding a
  connection open.
- `WriteTimeout` — symmetric body cap on the response.
- `IdleTimeout` — keep-alive ceiling.
- `MaxHeaderBytes` — header smuggling defence, default 1 MiB.

A post-merge audit found **six daemons still had listeners that
only set `ReadHeaderTimeout`** — the rest were zero / unlimited:

| Daemon | Listener | Bind | Missing knobs |
|---|---|---|---|
| `cmd/meterd/main.go` | metrics + healthz | `cfg.MetricsAddr` (loopback) | RT/WT/IT/MHB |
| `cmd/schedd/main.go` | metrics + /fcvm | `cfg.MetricsAddr` (loopback) | RT/WT/IT/MHB |
| `cmd/vmmd/main.go` | metrics + 3 siblings | `cfg.MetricsAddr` (loopback) | RT/WT/IT/MHB |
| `cmd/builderd/main.go` | metrics | `cfg.MetricsAddr` (loopback) | RT/WT/IT/MHB; **also: no Shutdown on for-loop main** |
| `cmd/imaged/main.go` | metrics | env-only `FAAS_IMAGED_METRICS_ADDR`, default 127.0.0.1:9102 | RT/WT/IT/MHB; **also: env-only config (no TOML field)** |
| `pkg/githubd/server.go` | webhook + metrics | 127.0.0.1:8083 (loopback) | RT/WT/IT/MHB (bodies up to 10 MiB — different defaults) |

The risk surface: a runaway scraper holding a connection open on
any of these listeners (no `ReadTimeout`), oversized headers from
a malformed scrape (no `MaxHeaderBytes`), or a half-open keep-alive
(no `IdleTimeout`) can pin a goroutine indefinitely. Loopback-only
by convention, but not loopback-enforced at the bind — a
misconfigured deployment exposing them on a non-loopback interface
is currently unmitigated.

The architectural question: **what should the canonical metrics-
listener shape be**, so a future daemon doesn't reinvent it (and
ship a half-applied variant)? This ADR names the shape; the
implementation lands in this same PR.

### Why this is an ADR (not a follow-up issue)

The four knobs + header cap are the same knob set apid and
gatewayd-public use today. There is no new failure mode, no new
customer-visible behaviour, no new cardinality. The decision is
"match what PR #996 did on apid/gatewayd-public, applied to the
remaining daemons." This is a **precedent worth naming** — without
an ADR, the next daemon author would have to grep the codebase to
discover the canonical shape and risk picking a different knob set.

## Decision

### 1. Two constant families in `pkg/api/limits.go`

The metrics-listener defaults live next to the existing
`APID*SecondsDefault` cluster (`pkg/api/limits.go:2099-2103`),
following the same "int-seconds to avoid the `time` import"
precedent:

```go
// Metrics-listener defaults (ADR-122, follow-on to issue #995).
MetricsReadTimeoutSecondsDefault  = 10 // loopback scrape cap
MetricsWriteTimeoutSecondsDefault = 10 // mirror
MetricsIdleTimeoutSecondsDefault  = 60 // matches apid /metrics keep-alive

// Webhook-listener defaults (githubd only, ADR-122).
WebhookReadTimeoutSecondsDefault  = 30 // 10 MiB upload budget at the readBody cap
WebhookWriteTimeoutSecondsDefault = 30 // mirror
WebhookIdleTimeoutSecondsDefault  = 60 // matches metrics
```

`MaxHeaderBytes` reuses `api.DefaultMaxHeaderBytes` (1 MiB) — the
existing constant that PR #996 introduced for apid's customer-facing
listener. No new constant.

The webhook variant uses `ReadTimeout=30s` instead of `10s` to
budget a slow GitHub webhook client uploading 10 MiB at githubd's
existing `readBody` cap (`pkg/githubd/server.go:323`). 30s is
~33 KiB/s, slow enough to tolerate any real GitHub webhook delivery
without burning the listener goroutine indefinitely.

### 2. Per-daemon `*MetricsListener()` helper

Each daemon's `Config` struct exposes a `MetricsListener()` method
that returns the resolved `(read, write, idle, maxHeaderBytes)`
tuple. The pattern is identical across the six daemons:

```go
func (c *Config) MetricsListener() (read, write, idle time.Duration, maxHeaderBytes int64) {
    read = c.MetricsReadTimeout
    if read == 0 { read = time.Duration(api.MetricsReadTimeoutSecondsDefault) * time.Second }
    // ... same for write, idle, maxHeaderBytes
    return
}
```

Four `time.Duration` TOML fields (`metrics_read_timeout`,
`metrics_write_timeout`, `metrics_idle_timeout`,
`metrics_max_header_bytes`) carry per-daemon overrides. Zero in
the TOML falls back to the constant — operators that don't need to
tweak the knob set get the canonical shape for free.

`pkg/githubd` does not get a helper. The webhook variant is
hardcoded against the `Webhook*SecondsDefault` family in
`pkg/githubd/server.go:158` because the listener is constructed in
the gRPC/HTTP fan-out function, not from a per-daemon Config. A
future per-deployment webhook knob lands via a config.go surface,
not a helper.

### 3. Listener construction

Every daemon's metrics listener now sets the five knobs:

```go
httpSrv = &http.Server{
    Handler:           mux,
    ReadHeaderTimeout: 10 * time.Second, // pre-existing; ADR-122 doesn't override
    ReadTimeout:       readTimeout,
    WriteTimeout:      writeTimeout,
    IdleTimeout:       idleTimeout,
    MaxHeaderBytes:    int(maxHeaderBytes), // http.Server field is int
}
```

`ReadHeaderTimeout` stays at 10s everywhere — the pre-existing
Slowloris guard. ADR-122 adds the four missing knobs; it doesn't
override what already exists.

### 4. imaged env → TOML alignment

`cmd/imaged/main.go` was previously env-only (no
`/etc/faas/imaged.toml`). A new `cmd/imaged/config.go` introduces
the canonical Config shape, mirroring meterd/schedd/vmmd/builderd.
The legacy `FAAS_IMAGED_METRICS_ADDR` env var becomes an overlay
(operator-side back-compat) via `Config.GetMetricsAddr(env)` —
mirrors the apid precedent at `cmd/apid/config.go::GetMetricsAddr`.

The default bind address stays `127.0.0.1:9102` (the legacy env-
only default). Empty disables the listener (same disable semantic
as the env-only path).

### 5. builderd shutdown drain

`cmd/builderd/main.go`'s main `for { select { ... } }` loop had no
`Shutdown` path for the metrics listener — `ctx.Done` returned
immediately and left `httpSrv` bound. The fix adds a 5s
`httpSrv.Shutdown(stopCtx)` on the `ctx.Done` arm (mirrors
`cmd/meterd/main.go:978-983`). Loopback-only bug; doesn't break
any existing test because tests don't depend on the listener's
lifecycle.

### 6. Spec & limits sync

- `pkg/api/limits.go` — the constants themselves carry doc
  comments with the rationale (above).
- `docs/faas_implementation_spec.md` §11 — new bullet under
  "Listener hardening": "All daemons serving metrics endpoints
  install the canonical metrics-listener shape (ADR-122)."

## Why no per-route timeout policies

The canonical shape is a single set of five knobs. Per-route
policies would need a new edge-rule shape (e.g. an
`EdgeRule` extension that admits a per-route timeout pair). This
is out of scope — the canonical shape's failure modes (runaway
scraper, oversized headers, half-open keep-alive) don't care which
route is being scraped, and adding per-route knobs would invite
operator-side drift away from the canonical shape.

A per-route knob is a reasonable follow-up — but it lands after
the canonical shape is observed.

## Why no TLS termination on metrics listeners

Loopback-only by convention; no TLS today. A deployment that needs
remote scrape mTLS lands a separate listener (and a separate ADR)
— bolting TLS onto the loopback canonical shape would muddy the
precedent and require per-daemon cert paths for no operational
benefit.

## Risks (numbered)

- **R1. Helper proliferation.** Six daemons now carry a
  `MetricsListener()` helper that's literally identical. The
  duplication is deliberate — extracting a shared helper would
  force every daemon to import a new package and would couple
  their Config types to a foreign shape. The duplication cost
  (~20 LoC × 5 = ~100 LoC) is dwarfed by the import-graph cost of
  factoring. If a future daemon needs a different shape (e.g. a
  different default), the helper is local to that daemon's
  config.go — no cross-package impact.
- **R2. imaged env-overlay dead code.** Operators on the legacy
  env-only path who set `FAAS_IMAGED_METRICS_ADDR=` keep working
  (the GetMetricsAddr overlay wins). Operators who only set the
  new TOML `metrics_addr` key get the same behaviour (TOML is
  the fallback). The two paths converge on `cfg.MetricsAddr`.
- **R3. int64 → int cast.** The `cfg.MetricsMaxHeaderBytes` field
  is `int64` to mirror `api.DefaultMaxHeaderBytes`; the
  `http.Server.MaxHeaderBytes` field is `int`. The cast at the
  listener site (`int(maxHeaderBytes)`) is fine on every platform
  Gregale targets (amd64, arm64); the value is in MiB, well below
  `INT_MAX`.
- **R4. imaged's existing tests.** Pre-ADR-122 imaged tests didn't
  exercise the metrics listener. The new `cmd/imaged/config_test.go`
  covers the Config surface (defaults + override + env overlay) —
  the listener shape is pinned via `MetricsListener()` rather than
  a runtime httptest.
- **R5. Test stability under cold runner.** The meterd factory
  test (`TestDefaultDeps_MetricsListenAndServe_AppliesCanonicalShape`)
  binds a real `127.0.0.1:0` listener and inspects the returned
  `*http.Server`. Cold-runner flakiness could surface on a heavily
  loaded CI box — the 2s Shutdown deadline is generous, and the
  test never asserts on scrape behaviour (only the struct fields),
  so the test is stable under load.
- **R6. meterd factory signature change.** The factory signature
  grew from 2 args to 6. The change is mechanical (existing stubs
  pass through the four timeouts as `_`), but every test in
  `cmd/meterd/main_test.go` that injects a `metricsListenAndServe`
  was updated. The wider signature is the price of keeping the
  factory cfg-independent (tests don't need to construct a Config).

## Cross-references

- ADR-009 (identical inner network world — the snapshot-reuse
  invariant that makes a per-plan cap viable — adjacent concept:
  the canonical shape makes a future per-route policy easier).
- ADR-070 (split public listener — gatewayd-public is the edge
  that enforces the timeouts; this ADR is about the loopback
  listeners on the control plane and compute nodes).
- ADR-121 (buffered response body cap — the precedent for naming
  envelope-layer contracts in ADRs rather than follow-up issues).
- Issue #995 (this ADR's mega-issue; PR #996 closed the customer-
  facing surface; this PR closes the loopback surface).
- Spec §11 ("Listener hardening" — new bullet under this section
  names the canonical shape).
- Spec §17 (gaps audit — the audit that surfaced this follow-up is
  recorded in §17 G7 lean).

## Post-merge audit (issue #995 closure)

The audit that confirmed the PR’s merge left three production HTTP
listeners with a partial knob set. This section records the closure:
each remaining site now installs the canonical shape, the knob it
adopts, and the rationale for any variant.

### Sites closed by the follow-on PR

| # | Site | Knob set adopted | Rationale |
|---|---|---|---|
| 1 | `cmd/gatewayd-public/main.go::buildServers` **publicSrv** (customer-facing TLS edge) | RHT=10s + RT/WT=`CustomerRequestEnvelopeTimeout` + **IT=120s** + MHB=1 MiB | Customer-facing variant. RT/WT permit the largest supported upload followed by guest execution; **IT=120s** bounds keep-alive time. |
| 2 | `cmd/gatewayd-public/main.go::buildServers` **controlSrv** (loopback :9092 healthz/readyz/metrics) | RHT=10s + RT=10s + WT=10s + IT=60s + MHB=1 MiB | Canonical metrics variant. The control mux is loopback-bound and serves only /healthz /readyz /metrics — the metrics variant is the right ceiling. RHT bumped from 5s to 10s for consistency with the rest of the canonical surface. |
| 3 | `pkg/gateway/control.go::NewControlHTTPServer` (the helper behind `RunControlServer`) | RHT=5s + RT=10s + WT=10s + IT=60s + **MHB=1 MiB** | RHT=5s stays tighter than the 10s canonical metrics variant because the control plane only serves probes / metrics — no slow-client body, no slow headers. The four timeout knobs pre-date this PR; **MHB=1 MiB is the new pin** (was 0, relying on stdlib default). Extracted from `RunControlServer` into the exported `NewControlHTTPServer` so a test can pin the struct fields without binding a real listener (mirrors the `pkg/githubd.NewWebhookHTTPServer` precedent). |
| 4 | `pkg/gateway/synth.go::NewSynthServer` (SynthServer — internal compute loopback) | RHT=5s + RT=10s + WT=10s + IT=60s + MHB=1 MiB | Canonical metrics variant; RHT=5s kept as the pre-existing H2C negotiation guard. The four missing knobs are the audit's Site 4 closure. Standalone construction site (future callers of `synth.New()` outside the Tier A7 unified mux inherit the same envelope). |

### Documented intentional exceptions

The audit flagged two more listeners that are intentionally NOT
hardened to the canonical shape:

| Site | Why excluded |
|---|---|
| `cmd/vmmd-stream-bridge/main.go:147` (per-guest H2C unix-socket bridge) | Per-guest H2C streams are long-lived by design; the bridge enforces an explicit session `deadline` per-LC-stream. Stdlib default timeouts are acceptable for these long-lived streams. |
| `cmd/gregale/templates/hello-go/main.go:28` (sample guest template) | In-guest demo code, not a production surface. The MaxHeaderBytes-missing knob is incidental. |

### Why this is an amendment, not a new ADR

The canonical shape is the load-bearing decision; the three sites
above are the last un-hardened details. Filing a separate ADR-123
would duplicate the rationale table and the per-daemon shape; an
amendment keeps the canonical-shape contract in one place. A future
extension (e.g. pprof listener, debug listener, debug-egress
listener) that needs a new variant should file a new ADR and link
this one.
