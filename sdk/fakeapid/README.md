# fakeapid

> Hermetic stand-in for the `apid` daemon, used as the smoke
> fixture for the public SDKs. Stdlib-only on purpose.

This binary mimics a small slice of the apid HTTP surface — the
five routes the SDK quick-start exercises, plus a `/__healthz`
liveness sentinel and a structured 404 Problem envelope. The
canonical wire shapes live in [`api/openapi.yaml`](../../api/openapi.yaml)
and the SDK DTOs in [`sdk/go/internal/api/dto.go`](../go/internal/api/dto.go).

## Build

```sh
go build -o bin/fakeapid .
```

## Run

```sh
PORT=8123 ./bin/fakeapid
# fakeapid listening on http://127.0.0.1:8123
```

Default port is `8123`. Bound to `127.0.0.1` only (not `0.0.0.0`).

## Routes

| Method | Path | Response |
|---|---|---|
| `GET` | `/v1/account` | `AccountResponse` (canonical plan=hobby account) |
| `POST` | `/v1/apps` | `AppResponse` (slug echoed from request body) |
| `GET` | `/v1/apps` | `[]AppResponse` (one element) |
| `GET` | `/v1/usage` | `[]UsageResponse` (one element — **array, not object**) |
| `GET` | `/v1/apps/hello` | `AppResponse` with `slug=hello` |
| `GET` | `/v1/apps/hello-world` | `AppResponse` with `slug=hello-world` |
| `GET` | `/v1/apps/missing-app-404` | **404 `application/problem+json`** with `code: "not_found"` (canonical sentinel) |
| `GET` | `/__healthz` | `{"ok": true}` (liveness) |

Anything else returns 404 with an `application/problem+json`
envelope (RFC 7807 fields: `type`, `title`, `status`, `code`,
`detail`, `tx_id`).

## Auth

**Permissive.** Any `Authorization: Bearer <x>` header is accepted;
a missing header is also accepted. The fixture exists to validate
wire shapes, not auth — the real daemon enforces auth, this one
does not.

## Used by

- `sdk/go` smoke tests (PR 4) — `go test -count=1 ./...`
  builds and spawns the binary.
- `sdk/node` smoke tests (PR 5) — `child_process.spawn` of
  `./bin/fakeapid` from a Node test runner, with
  `-tags smoke_bin` artifact check.
- `sdk/python` smoke tests (PR 6) — `subprocess.Popen` of
  `./bin/fakeapid` from a pytest runner, same artifact check.

## Why stdlib-only

`go.mod` declares no requires. The fixture must not depend on
`github.com/poyrazK/faas/sdk/go` (the SDK module) or any other
non-stdlib package, because Node and Python SDKs spawn the
*same* binary from their own CI without a Go module dependency.

## CI

The `sdk-fakeapid` job in `.github/workflows/ci.yml` runs
`go build -o bin/fakeapid . && go test -count=1 -race ./...`
in this directory.

**Required follow-up for PR 5 / PR 6**: their CI jobs MUST invoke
`go test -tags smoke_bin -count=1 -run TestPreBuiltBinary ./...`
from this directory after building the artifact (the `sdk-fakeapid`
job already produces `./bin/fakeapid`). The `smoke_bin` build
tag is a separate compilation unit that spawns the pre-built
binary rather than re-compiling — without it, a regression that
breaks the shipped artifact while leaving source compilable would
go undetected. The `sdk-fakeapid` job's un-tagged tests do not
cover this path (they call `buildFixtureBinary` in-test).
