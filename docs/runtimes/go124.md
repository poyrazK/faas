# `go124` runtime

Go 1.24 on both deploy surfaces — apps (long-running, static binary in
an OCI image layer) and functions (per-request subprocess, §4.9 envelope
contract). Built by Railpack's Go provider; detected from
`go.mod` (priority: `docker > node > python > go`).

## Function contract

The customer's source is a Go package with `go.mod` and a `main` that
reads the §4.9 request envelope from **stdin** and writes the §4.9
response envelope to **stdout**. There is no HTTP server in the handler
— the `go124` runner is the HTTP server inside the microVM (listens on
`:8080`) and execs the compiled handler binary at `/app/handler` per
request.

For source builds, imaged reads the compiled executable path from the exported
OCI image configuration and normalizes it to `/app/handler`. Railpack 0.38
exports `/app/out` with working directory `/app` and a `bash -c ./out` launch
command. Older exports without process metadata retain the `/app/server`
fallback. Image assembly only accepts a literal executable inside `/app`;
it does not execute shell expressions or follow symlinks outside the image.
This normalization applies to both `go124` and `go124-alpine`.

The runner shim sets `FAAS_RUNTIME=go124` in the handler's environment
so customers can branch on runtime if they want.

### Minimal handler

```go
package main

import (
	"encoding/base64"
	"encoding/json"
	"io"
	"os"
)

type request struct {
	Method  string            `json:"method"`
	Path    string            `json:"path"`
	Headers map[string]string `json:"headers"`
	Query   string            `json:"query"`
	BodyB64 string            `json:"body_b64"`
}

type response struct {
	Status  int               `json:"status"`
	Headers map[string]string `json:"headers"`
	BodyB64 string            `json:"body_b64"`
}

func main() {
	raw, _ := io.ReadAll(os.Stdin)
	var req request
	_ = json.Unmarshal(raw, &req)

	body, _ := json.Marshal(map[string]string{"hello": "world"})

	_ = json.NewEncoder(os.Stdout).Encode(response{
		Status:  200,
		Headers: map[string]string{"content-type": "application/json"},
		BodyB64: base64.StdEncoding.EncodeToString(body),
	})
}
```

### Local smoke test

The §4.9 envelope round-trips with bash and `base64` — the runner is a
JSON-to-stdio translator, so:

```
echo '{"method":"GET","path":"/hello","headers":{},"query":"","body_b64":""}' \
  | go run main.go
```

prints a JSON response envelope on stdout. The platform runs the same
binary in production; nothing else differs.

### CGO

Railpack's `--plan go` defaults to `CGO_ENABLED=0` — the standard case
Just Works. Customers who need CGO (SQLite bindings, etc.) must ensure
the base image ships the libc their bindings link against.

- `runtime: go124` (default) — base is Chainguard Bash on Wolfi (glibc),
  compatible with binaries emitted by the Bookworm builder without carrying
  the Go compiler or build utilities.
- `runtime: go124-alpine` (opt-in) — base is `golang:1.24-alpine`
  (musl). See "Alpine variant" below for the libc-match contract.

## App contract

Customer source has the same `go.mod` + `main.go` shape as the function
contract, but `main` runs its own HTTP server (typically on `:3000`).
Railpack emits a static binary at `/app/server` and the OCI image's
`Cmd: ["/app/server"]` lands in the layer manifest via
`manifestFromImageConfig` in `pkg/imaged/handler.go` — no customer
wiring required. This is the **first runtime on the app path whose
entrypoint is a static binary**; previously every runtime went through
a runner-scaffold manifest.

If `manifestFromImageConfig` ever flips to reading `cfg.Entrypoint`
instead of `cfg.Cmd`, the manifest will be empty and validation will
fail loudly. This contract is now pinned by
`TestManifestFromImageConfig_AppModeCmd` (positive:
`cfg.Cmd = ["/app/server"]` produces the manifest entrypoint verbatim,
plus the defensive-copy pin: mutating `cfg.Cmd[0]` after conversion
does NOT mutate `manifest.Entrypoint[0]`, because the function uses
`slices.Clone(cfg.Cmd)` to force a fresh backing array)
and `TestManifestFromImageConfig_NoCmdYieldsEmptyEntrypoint` (negative:
an image without `Cmd` produces an empty entrypoint that fails
`manifest.Validate()` with `"empty entrypoint"`). Both tests live in
`pkg/imaged/handler_test.go`.

## Base image

- Base ref: `ghcr.io/onebox-faas/runner-go124:latest`
- Source: `images/runner-go124.Dockerfile`
  (`FROM cgr.dev/chainguard/bash:latest`, digest-pinned for linux/amd64)
- The final runtime base keeps the matching glibc libraries but omits the Go
  compiler/toolchain. The two-drive scheme shares that base across all
  `go124` apps; per-app cost is just the static binary (~5–30 MB).

### Operational configuration

The runtime base is **auto-staged** by `imaged` through
`pkg/imaged/base_stage.go::EnsureRuntimeBase`. The deployment pipeline
must write `FAAS_DEPLOY_BASE_REF_GO124` as an immutable OCI digest in
`/etc/faas/runtime-bases.env`; the default `:latest` is for unnamed
development daemons only.

The deployment pipeline publishes the image, records its config digest,
renders that digest into the node configuration, and starts `imaged`.
`imaged` pulls, validates, and stages the ext4 automatically; subsequent
boots short-circuit on the digest sidecar. Operators must not build, copy,
or manually place a runtime `.ext4` on a compute node.

## Alpine variant (opt-in)

For customers running a `go124` app on the alpine runtime id, the base
rootfs switches from Wolfi glibc to `golang:1.24-alpine` (musl). The Alpine
variant remains smaller because it uses musl, and both final images omit the
Go compiler/toolchain.

**Customer opt-in:** set `runtime: go124-alpine` on the function or app
manifest. The platform resolves the base via `pkg/imaged/base.go::baseRefFor`.
The default `go124` keeps its glibc runtime contract, so existing binaries
built against the Bookworm toolchain remain compatible.

**CGO constraint:** customers with cgo bindings (e.g.
`mattn/go-sqlite3`) must ensure their bindings link against musl —
rebuild the binary against `FROM golang:1.24-alpine AS build` in their
Dockerfile. `CGO_ENABLED=0` (Railpack's default) works on both bases;
the alpine variant is a drop-in for the common case. The libc
mismatch surfaces as `exec format error` on first wake — see the
failure-mode table below.

**Deployment configuration:** auto-staged by imaged the same way as the
bookworm base. The deployment pipeline must set
`FAAS_DEPLOY_BASE_REF_GO124_ALPINE` to a digest-pinned production ref;
operators do not build, copy, or manually place its `.ext4` on a node.

**Source:** `images/runner-go124-alpine.Dockerfile`
(`FROM golang:1.24-alpine`).

**Migration:** `00043_app_runtime_go124_alpine.sql` widens the
`apps_runtime_check` constraint to accept `'go124-alpine'` alongside
the older three runtimes. No default-flip is performed in this PR.
Future PRs may flip the default once fleet-wide `snapshot_fleet_avg_mb`
is measured with both bases co-resident
(`pkg/api/limits.go::FleetSnapshotAvgTargetMB = 130`, alarm 160).

## Detection priority

`pkg/builderd/detect.go` priority order is
`docker > node > python > go`. A tarball containing both a `Dockerfile`
and a `go.mod` builds as an image (buildctl), not a Railpack go app.
A tarball with both `go.mod` and `requirements.txt` builds as a Python
app (Railpack python plan) — if the customer really wants Go, drop
`requirements.txt`.

## Failure modes

| Symptom | Cause | Fix |
|---|---|---|
| `unknown archive shape` | no `go.mod`, `package.json`, `requirements.txt`, or `Dockerfile` | add `go.mod` |
| `railpack: plan "go" failed` | compiler or build-plan failure | inspect the build log and correct the reported build error |
| handler exec fails with `exec format error` | CGO binary mismatched with base libc | rebuild with the matching libc (bookworm/alpine) |
| `exec format error` on first wake, alpine runtime | cgo binary links glibc, musl base rejects it | rebuild against `FROM golang:1.24-alpine AS build` in customer Dockerfile |
| `unsupported function runtime "go124-alpine"` | migration `00043` not applied, or the deployment pipeline has not supplied a valid digest-pinned base ref | apply the migration and repair the node's rendered base-ref configuration; imaged stages the base automatically |
| `app.Cmd empty` manifest error | `manifestFromImageConfig` regression | revert the field flip in `pkg/imaged/handler.go` |

## See also

- `docs/STATUS.md` — runtime roster
- `pkg/api/build.go::FrameworkRailpackGo` — wire contract
- `pkg/builderd/dispatch.go::MapFramework` — detection → wire
- `guest/init/main_linux.go` — in-VM `--plan go` dispatch
- `guest/runners/go124/main.go` — function runner shim
- `images/runner-go124.Dockerfile` — base image

<!-- CI status: migration 00037_app_runtime_go124.sql verified on PR #201; 00043_app_runtime_go124_alpine.sql added in Tier 2 PR. -->
