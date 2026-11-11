# ADR-20261111140000: Run ordinary HTTP apps with `gregale dev --local`

Date: 2026-11-11
Status: Accepted
Related: ADR-20261107210000 (local functions), ADR-1170 (development commands)

## Context

Local functions already share remote resource attachment and offline Docker
Postgres/S3 setup. Ordinary HTTP applications cannot use this workflow because
the entry point always plans functions. Developers should not need to rewrite
an Express, FastAPI, or Go server as a function to use local resources.

## Decision

- Keep explicit function manifests and inferred handlers on the existing
  production adapter/runner path. Invalid function manifests fail validation.
- For other source trees, resolve an HTTP app command from an explicit
  `--watch-command`, `dev.watch.command`, shared framework development-command
  detection, or the existing inferred start command, in that order.
- Launch that command locally in an owned Unix process group. Set `HOST` and
  `PORT` for an ephemeral loopback backend and expose a stable loopback proxy
  at the requested local port. Known Python framework commands receive explicit
  listening options. Dependencies must already be installed; project tool and
  virtual-environment directories are prepended to PATH.
- Reuse the function path's resource attachment, environment layering and
  cleanup. No customer source is uploaded. Runtime host/port settings override
  user values so they cannot redirect the local proxy.
- Restart after source edits settle using the existing deploy-source ignore
  and fingerprint rules. Re-resolve the command after configuration edits.
  Crashes remain observable and recover on the next edit rather than looping.
  `--no-reload` disables CLI restarts, not a custom command's own watcher.
- Stream HTTP responses and support upgrades through Go's reverse proxy. Drop
  client-supplied platform headers and the local function-trigger token. HTTP
  apps receive no function-trigger endpoint.
- Stop the entire owned process group on reload, command exit, or cancellation,
  including descendants that ignore graceful termination. Local HTTP app
  process management is supported on Unix platforms; remote `gregale dev`
  remains available elsewhere.

## Consequences

Existing servers can use local Postgres/bucket bindings without handler
wrappers. The local process remains outside Gregale's microVM, quotas, pause,
and snapshot semantics; remote development remains the parity check. Arbitrary
custom commands and package scripts must honor PORT. An app still building or
not listening receives an explicit unavailable response rather than a false
successful request verification.
