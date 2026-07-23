# OpenAPI spec + CI gate

**Status:** shipped behind a CI gate (`make spec-check`). Spec lives at
[`api/openapi.yaml`](../api/openapi.yaml); the same bytes are embedded
into `apid` and served at `GET /v1/openapi.{yaml,json}`.

## What this is

A hand-authored **OpenAPI 3.1** description of the customer-facing
`/v1/*` REST surface, plus a CI gate that fails any PR where the
spec, the routes, the DTOs, or the `Code*` constants drift apart.
This is the same "regenerate-and-diff" pattern the repo already runs
for `proto-check`, `sqlc-check`, and `egress-check` (Makefile:64,
Makefile:190, Makefile:140).

## Source-of-truth principle

| Surface            | Source of truth              |
| ------------------ | ---------------------------- |
| Documentation      | `api/openapi.yaml`           |
| Behavior           | `cmd/apid/server.go`, `pkg/api/dto.go`, `pkg/api/errors.go` |
| Verification       | `cmd/apid/spec_compliance_test.go` (AST walker) + `vacuum` lint |

The spec is **not** generated from the code. Generating it would lose
response codes and per-route headers (`Idempotent-Replayed`,
`Retry-After`) — handler signatures can't carry those. The hand-authored
spec is the documentation; the AST walker keeps it honest.

## What's in the spec

- 25 customer routes (account, apps, deployments, instances, domains,
  crons, keys, secrets, usage).
- 2 self-description routes (`/v1/openapi.yaml`, `/v1/openapi.json`).
- 25+ schemas mirroring `pkg/api/dto.go`, `pkg/api/secrets.go`,
  `pkg/api/appmanifest.go`.
- 18 named responses, including `TooManyRequests` which intentionally
  declares **two** content types — see the §429 note below.

## What's intentionally excluded

The compliance test maintains an exclude map
(`cmd/apid/spec_compliance_test.go:41`) for routes that are public
surface but not customer API:

| Route                         | Reason                                  |
| ----------------------------- | --------------------------------------- |
| `GET /v1/account/dpa`         | Public markdown (no auth).              |
| `POST /v1/webhooks/stripe`    | HMAC-signed webhook (operator-side).   |
| `GET\|POST\|DELETE /v1/compute-nodes` | Operator-only (ADR-029).         |
| `GET /v1/events`              | SSE — cookie OR Bearer, not `s.auth`.  |
| `GET\|POST /login`, `POST /logout`, `GET /auth/verify` | Dashboard magic-link.       |
| `GET /oauth/callback`         | GitHub App install callback.            |
| `GET /dashboard*`             | HTML dashboard chrome.                  |
| `POST /v1/cli-auth/code`      | CLI device-code mint.                   |
| `POST /v1/cli-auth/exchange`  | CLI device-code exchange.               |
| `GET\|POST /cli-auth`         | Dashboard claim form.                   |
| `GET /status`, `GET /status/slo.json` | Public status page.             |
| `GET /healthz`                | Loopback infra probe (issue #85).       |

The CLI auth flow (`/v1/cli-auth/*`) is documented separately in the
CLI command reference; it is anonymous on purpose (the CLI hasn't
authenticated yet) and lives outside the customer `/v1/*` API contract.

## The two-shape 429

The `TooManyRequests` response intentionally declares **both**:

- `application/problem+json` — emitted by the code-driven 429s in
  `pkg/api/errors.go`: `plan_limit_concurrency`, `quota_exhausted`.
  These use the standard RFC 7807 `Problem` envelope and include a
  stable `code` field.
- `text/plain` — emitted only by the authlimiter middleware
  (`pkg/middleware/authlimit.go`). It is a rate limiter for unauth
  traffic, doesn't have an account scope, and uses Go's stdlib
  `http.Error` for the response body.

Both paths are intentional. The spec must reflect both because the
gate asserts every `Code*` constant maps to a documented status — and
because SDK generators will not validate against a spec that hides a
real response shape.

## CI gate

`make spec-check` runs three things, in order:

1. **`vacuum lint -r api/vacuum.yaml api/openapi.yaml`** — style + rule
   checks via [`daveshanley/vacuum`](https://github.com/daveshanley/vacuum)
   (single Go binary, no Node, pinned to `v0.29.10` via `go install`).
   The ruleset lives at [`api/vacuum.yaml`](../api/vacuum.yaml); it
   extends `spectral:oas` and overrides the rules that don't apply to
   a single-box deployment (`oas3-api-servers: off`) while turning
   shape rules into errors (`operation-tag-defined`,
   `oas3-valid-media-example`, `no-eval-in-markdown`).
2. **`go test -race -count=1 -run TestSpecCompliance ./cmd/apid/...`** —
   three AST subtests:
   - `Routes` — walks `cmd/apid/server.go`'s `mux.HandleFunc(...)`
     calls, applies the exclude map, asserts parity against
     `paths[*][method]` in the spec.
   - `Schemas` — walks `pkg/api/*.go` for exported structs with
     `json:"name"` tags, asserts schema parity.
   - `ErrorCodes` — walks `pkg/api/errors.go` for `Code*` constants
     and `StatusForCode` switch cases, asserts the spec has a
     matching response with `application/problem+json` for every
     status.
3. **`git diff --exit-code`** on `api/openapi.yaml`,
   `pkg/apid/openapi.yaml`, and `api/vacuum.yaml` — fails if the
   working tree has uncommitted changes that the AST test didn't
   catch.

Drift is bi-directional: adding a route without a spec entry fails
CI, and adding a spec entry without a route also fails CI. The gate
catches both.

## Spec hosting

`apid` embeds the spec at build time via `//go:embed`:

- [`pkg/apid/openapi_handler.go`](../pkg/apid/openapi_handler.go) owns
  the embed and the two handlers.
- [`pkg/apid/openapi.yaml`](../pkg/apid/openapi.yaml) is a generated
  copy of `api/openapi.yaml`. `go:embed` only resolves paths inside
  the package directory, so the copy is required.
- `make spec-sync` (a prerequisite of `spec-check`) refreshes the
  copy. The copy is checked in so the binary is self-contained and
  a stale CI cache can't ship mismatched bytes.
- `GET /v1/openapi.yaml` and `GET /v1/openapi.json` are mounted at
  the end of `cmd/apid/server.go`'s `handler()` table. They are
  anonymous (no `s.auth`/`s.authLimited` wrapper) and emit
  `Cache-Control: public, max-age=300`.

The JSON sibling parses the YAML at request time and re-emits it via
`encoding/json`. SDK generators (`openapi-generator`,
`oapi-codegen`) prefer JSON; `curl` users get the YAML form.

## Why this isn't `oapi-codegen`

`oapi-codegen`'s server stub generation reads the spec and emits
handler skeletons. The repo's handlers are `func(http.ResponseWriter,
*http.Request)` wrappers around state.Store + middleware chains;
regenerating stubs would lose response codes, headers, and middleware
composition. The handler signatures can be hand-patched per-endpoint,
but that's the same maintenance burden as hand-authoring the spec —
without the indirection. The AST walker reading JSON tags directly
covers ~90% of what codegen would, with zero rework risk during M8.

## Day-2 follow-ups

- **Promote warnings to errors.** `vacuum.yaml` currently keeps
  `operation-description`, `tag-description`, `info-contact`, and
  `info-license` at `warn`. After polishing all the prose, flip them
  to `error` so PRs can't ship sparse descriptions.
- **Examples.** `oas3-missing-example` (87 warnings) is the loudest
  signal. Add `example:` blocks to the response shapes — SDK codegen
  reads them and ships nicer stubs.
- **External docs site.** `docs/faas_ux_spec.md:81` references
  `https://docs.DOMAIN/...` for the rendered HTML home. When that
  lands, point a Redoc/Starlight build at `/v1/openapi.json` instead
  of maintaining a separate HTML description.
