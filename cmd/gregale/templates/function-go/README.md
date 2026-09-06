# function-go

A minimal Go 1.24 function handler.

Functions differ from apps in two ways:

1. No HTTP server — the go124 runner is the HTTP server (listens on
   `:8080` inside the microVM) and execs your compiled handler at
   `/app/handler` per request. The runner pipes the §4.9 envelope
   into stdin; your handler writes the §4.9 response envelope to
   stdout.
2. CLI forces `--runtime go124 --handler handler.go` so the wiring
   is automatic. You don't need to know those flags.

The handler compiles to a static binary (Railpack's go plan defaults
to `CGO_ENABLED=0`); no interpreter is involved at request time.

The starter directory intentionally has no `go.mod`: that keeps a later plain
`gregale deploy` detectable as a function. The CLI adds a minimal module file
to the upload archive without changing your local files.

## Deploy

```
gregale deploy --template function-go
```

## Invoke

```
gregale open   # browser test page, or POST from any HTTP client
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
expects. The platform runs the same handler binary in production;
nothing else differs.
