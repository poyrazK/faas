# gregale-go — Gregale Go SDK

> **PR 3 of issue #266** — the public Go SDK surface. The module is
> still internal to the monorepo and consumed only by the daemon's
> own tests; **publishing to the Go module proxy is gated on PR 13**.

This is the public import path for the Gregale platform:

```go
import faas "github.com/poyrazK/faas/sdk/go"
```

The package exposes:

- a typed `Client` with **57 methods** covering every apid route,
- bearer-auth + caller-supplied `Idempotency-Key` for replay safety,
- RFC 7807 error envelope + `errors.Is(err, faas.ErrNotFound)` sentinels,
- cursor pagination helpers (`ListDeploymentsAll`),
- SSE streaming via `Decoder` for app logs, deployment logs, and events,
- functional `Option` for HTTP transport, retry, and logger.

## Install

```sh
go get github.com/poyrazK/faas/sdk/go
```

The SDK targets `go 1.23` (the floor of the daemon's own toolchain
at the moment of extraction). The daemon's `go.mod` is `go 1.25.7`,
but the SDK stays on 1.23 so a customer pinned to an older Go
toolchain can still consume it.

## Quick start

```go
package main

import (
    "context"
    "errors"
    "fmt"
    "log"
    "os"

    faas "github.com/poyrazK/faas/sdk/go"
)

func main() {
    c, err := faas.NewClient("https://api.example.com", os.Getenv("FAAS_TOKEN"))
    if err != nil {
        log.Fatal(err)
    }

    app, err := c.GetApp(context.Background(), "hello-world")
    if errors.Is(err, faas.ErrNotFound) {
        log.Fatal("app not found")
    }
    if err != nil {
        log.Fatal(err)
    }
    fmt.Println(app.Slug, app.Status)
}
```

## Idempotency

Every mutating call (POST/PATCH/DELETE) carries an `Idempotency-Key`
header. The SDK **auto-mints a UUIDv4** if you don't supply one, so
naive `c.CreateApp(...)` calls are replay-safe out of the box.

For retries that need a stable key, pin one explicitly:

```go
ctx = faas.WithIdempotencyKey(ctx, "deploy-attempt-3")
dep, err := c.Deploy(ctx, slug, req)
```

A retried call with the same key returns the cached response from
the server's replay middleware (24h window).

## Errors

Every 4xx/5xx with a Problem-shaped body returns `*faas.APIError`:

```go
app, err := c.GetApp(ctx, "missing")
if err != nil {
    var apiErr *faas.APIError
    if errors.As(err, &apiErr) {
        log.Printf("api: %s (%s)", apiErr.Problem.Code, apiErr.Problem.Detail)
    }
    // Or, for common cases:
    if errors.Is(err, faas.ErrNotFound) { ... }
    if errors.Is(err, faas.ErrRateLimited) { ... }
    if errors.Is(err, faas.ErrCapacity) { ... }
    if errors.Is(err, faas.ErrUnauthorized) { ... }
}
```

## Options

> **Three options are reserved until PR 12.** `WithBaseURL`,
> `WithToken`, and `WithDeployTimeout` return
> `errOptionUnsupported` today — the internal SDK's
> `baseURL` / `token` / `deployHTTP` fields are unexported and
> can't be mutated through the public wrapper yet. PR 12 promotes
> those fields and un-deprecates these options. Until then, callers
> needing to switch base URL, rotate a token, or set a deploy
> timeout must reconstruct the `Client` via `NewClient`.

```go
c, err := faas.NewClient(baseURL, token,
    faas.WithHTTPClient(myHTTPClient),           // custom transport
    faas.WithRetry(3, 200*time.Millisecond),     // bounded retry on 5xx/429 (PR 4)
    faas.WithLogger(slog.Default()),              // request/response logging (PR 4)
)
```

## Local development

```sh
cd sdk/go
go build ./...
go vet ./...
go test ./...
```

The CI gate is `.github/workflows/ci.yml::sdk-go` — a separate job
that runs `go build`, `go vet`, and `go test` inside `sdk/go/`. The
daemon's own `make test` walks only the daemon's package tree, so
the SDK needs its own gate (memory: `nested-go-module-needs-own-ci-gate`).

The module is a leaf: it imports only Go stdlib + the internal
`api` package. PR 12 trims `pkg/api/*` (in the daemon's main module)
to its server-only files; this module then becomes the canonical
home for the wire DTOs.

## Reference

- godoc: run `go doc -all ./...` from this directory.
- OpenAPI spec: `../../api/openapi.yaml` (canonical), `../../pkg/apid/openapi.yaml` (embedded).
- ADR-038 (issue #266): documents the split contract between the SDK and the daemon.
- PR plan: `/.claude/plans/lets-create-imp-plan-bubbly-engelbart.md` (the 14-PR sequence).
