# `binary` runtime

One statically linked Linux executable, in any compiled language (Rust,
Zig, C, C++, Go, …), served as a function (ADR-987). The customer supplies
a Dockerfile that builds the executable; Gregale keeps only that file and
runs it behind the same runner, envelope contract and lifecycle as a
`go124` function.

`binary` is a function runtime. A long-running server that ships its own
Dockerfile is already an ordinary app and needs no runtime.

## Deploy

```bash
gregale deploy --function --runtime binary
```

or start from the template, which records `runtime: binary` in
`gregale.yaml`:

```bash
gregale init --template function-rust --path ./hello-rust
cd hello-rust && gregale deploy
```

The runtime is never inferred from source: a Dockerfile alone deploys as
an app. A `binary` deploy without a Dockerfile at the source root is
rejected (`Dockerfile required`); a Dockerfile on any other function
runtime is rejected too (`Dockerfile functions unsupported`).

## Build contract

The Dockerfile is built in an ephemeral builder microVM (ADR-003) with
BuildKit's Dockerfile frontend, under the usual build caps (2 vCPU,
2048 MB, 15 min). From the resulting image imaged takes exactly one file:

1. The program named by the image's `ENTRYPOINT` (or `CMD` when there is
   no entrypoint). A relative path resolves against `WORKDIR`, default
   `/app`.
2. It must be a literal path, with no shell expression, and must resolve
   inside `/app`. Symlinks are followed only inside the image.
3. The file is copied to `/app/handler` in the app layer. Everything else
   in the image is discarded, so the executable must not depend on files,
   libraries or a dynamic loader from its build image.

A multi-stage Dockerfile ending in `FROM scratch` with
`COPY --from=build … /app/handler` and `ENTRYPOINT ["/app/handler"]` is the
intended shape; the `function-rust` template is one.

Build for Linux on the node's architecture (x86_64 today) and link
statically: a musl target for Rust and C/C++, `-target x86_64-linux-musl` for
Zig, `CGO_ENABLED=0` for Go. The app layer limit for the plan applies to the
executable.

## Function contract

Identical to `go124` functions, see
[the go124 function contract](go124.md#function-contract): the runner is the
HTTP server on `:8080` and talks to the executable over stdin/stdout, one §4.9
request envelope per line in and one response envelope per line out.

- **Persistent handlers.** An executable containing the bytes
  `FAAS_PERSISTENT_PROTOCOL_V1` that writes `{"__faas_ready":true}` as its
  first stdout line when `FAAS_PERSISTENT_WORKER=1` is started before
  readiness and reused for every request; up to four processes serve
  concurrent requests. Any other executable runs once per request.
- **Snapshot restore.** A compiled program has no reseed hook, so on every
  restore the runner replaces its handler processes before the instance
  serves traffic. Restored instances never share random state
  (GHSA-24j2-p895-mwc9, ADR-687).
- **Output.** stdout carries only response envelopes; log to stderr, which
  reaches `gregale logs`.

The handler's environment has `FAAS_RUNTIME=binary`. Responses carry
`X-Faas-Guest-Runtime: binary`, and request telemetry and the runner metrics
label the runtime `binary`.

## Base image

`binary` has no base image of its own: it runs on the `go124` runner base
(`base/runner-go124-<arch>.ext4`) with the `go124` runner started as
`faas-runner --runtime binary`. `api.FunctionBaseRuntime` maps the runtime
to `go124` wherever a base or runner is selected, so the base is shared
exactly as for `go124`: auto-staged by imaged through
`pkg/imaged/base_stage.go::EnsureRuntimeBase` from the digest-pinned
`FAAS_DEPLOY_BASE_REF_GO124`. There is no `binary` base ref to configure,
and per-app disk cost is the executable alone. That base ships a libc, but it
is not part of the contract: a dynamically linked executable may start
today and break when the base changes.

Dockerfile builds record no runtime base, so a `binary` deployment pins no
runtime release.

## Failure modes

| Symptom | Cause | Fix |
|---|---|---|
| `Dockerfile required` | `runtime: binary` without a Dockerfile at the source root | add the Dockerfile |
| `Dockerfile functions unsupported` | a Dockerfile on another function runtime | set `runtime: binary`, or remove the Dockerfile |
| `compiled function executable must be inside /app` | `ENTRYPOINT` names a path outside `/app` | copy the executable to `/app/handler` |
| `compiled function build command must be a literal executable path` | shell-form or templated `ENTRYPOINT` | use the exec form, `ENTRYPOINT ["/app/handler"]` |
| `exec format error` or `no such file or directory` on the first request | dynamically linked, or built for another architecture | link statically for Linux x86_64 |
| `handler prewarm unavailable` in the runner log, then 500s | executable contains the protocol marker but never writes the ready line | write `{"__faas_ready":true}` first when `FAAS_PERSISTENT_WORKER=1` |
| build killed at 15 min | release build too slow for the builder VM | cache dependencies in an earlier layer, reduce LTO, or build fewer crates |

## See also

- `docs/adr/987-binary-function-runtime.md` — decision record
- `docs/runtimes/go124.md` — the shared runner and protocol
- `cmd/gregale/templates/function-rust/` — Rust template
- `cmd/e2e/binary_function_metal_test.go` — metal test: template build,
  gateway request, and handler replacement across snapshot restores
- `pkg/imaged/go_function_artifact.go` — executable selection
- `guest/runners/go124/main.go` — runner shim (`--runtime binary`)
