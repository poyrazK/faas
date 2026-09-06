# ADR-156 · Integrated developer runtime feedback

- **Status:** accepted
- **Date:** 2026-09-05
- **Decision:** attach one app-level runtime log stream to a watching
  `gregale dev` session after its first live sync; keep it connected across
  source redeploys and make it opt-out with `--no-logs`.

## Context

The developer loop already reports source transfer, build stages, and the
stable URL. Once the deployment became live, however, a developer had to open
a second terminal and run `gregale logs` to see startup crashes, request
errors, or application output. That split the most important feedback loop at
the point where the remote environment was supposed to feel local.

## Decision

Watch mode opens `StreamAppLogs` for the stable developer app after the first
successful live sync. The stream is app-scoped rather than deployment-scoped,
so it naturally follows the current live version after a later latest-save-wins
redeploy. Runtime lines are rendered as `runtime stdout | ...` or
`runtime stderr | ...` and remain separate from deployment/build progress.

The CLI reconnects with a bounded backoff after a transient stream, scheduler,
or API interruption. Ctrl-C and watcher shutdown cancel the stream. `--no-logs`
disables the attachment for terminal multiplexers and scripts; `--once` never
attaches a long-lived stream.

## Consequences

The normal edit → build → live → observe loop stays in one terminal. A stable
app stream avoids cursor bookkeeping and does not need to be recreated for
every source sync. Runtime output is intentionally human-readable and is not
emitted when global JSON mode is active.

The stream is best-effort observability: an unavailable log backend does not
fail a successful developer deployment, and reconnect warnings explain the
degraded state while the watcher continues.
