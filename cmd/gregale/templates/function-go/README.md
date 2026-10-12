# function-go

A minimal Go function handler built with Gregale's patched Go 1.26.9 starter toolchain.

Functions differ from apps in two ways:

1. No HTTP server — the go124 runner is the HTTP server (listens on
   `:8080` inside the microVM) and talks to your compiled handler at
   `/app/handler` over stdin/stdout: one §4.9 request envelope per
   line in, one response envelope per line out. This template speaks
   the persistent protocol, so the runner starts it once and reuses
   the process for every request; setup done in `main()` before the
   loop is paid once, not per request.
2. CLI forces `--runtime go124 --handler handler.go` so the wiring
   is automatic. You don't need to know those flags.

The handler compiles to a static binary (Railpack's go plan defaults
to `CGO_ENABLED=0`); no interpreter is involved at request time.

The starter directory intentionally has no `go.mod`: that keeps a later plain
`gregale deploy` detectable as a function. The CLI adds a minimal module file
to the upload archive without changing your local files.

## Deploy

```
gregale deploy --template function-go --name <slug>
```

## Invoke

```
gregale open <slug>   # browser test page, or POST from any HTTP client
```

## Local test (no platform)

The §4.9 envelope round-trips end-to-end with `bash` and `base64` —
the runner is just a JSON-to-stdio translator, so a quick smoke
test is:

```
echo '{"method":"GET","path":"/hello","headers":{},"query":"","body_b64":""}' \
  | go run handler.go
```

You'll see a JSON object on stdout with the same shape the runner
expects. Set `FAAS_PERSISTENT_WORKER=1` and send several lines to see
the persistent mode: a ready line, then one response per request. The platform runs the same handler binary in production;
nothing else differs.
