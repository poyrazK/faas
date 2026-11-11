# ADR-20261111162000: Reload selected local development environment files

Date: 2026-11-11
Status: Accepted
Related: ADR-20261107210000 (local functions), ADR-20261111140000 (local HTTP apps)

## Context

Local development reads its environment file at startup. Source restarts keep
those values, and function worker pools retain the initial environment for a
command. An edited configuration file therefore does not reach running apps.

## Decision

- Use explicit `--env-file`, including an explicit empty opt-out, before the
  manifest's `dev.env_file`. Resolve flag paths from the invocation directory
  and manifest paths from the source root. Capture the selected path at startup.
- Watch the selected file's content independently of source ignore rules and
  location. Settle edits, then validate the complete file before replacing any
  running process. Invalid or unavailable files keep the current processes;
  another edit retries. `--no-reload` disables automatic restarts.
- Rebuild each environment from the original shell/resource bindings and the
  current file. Function declarations retain precedence; HTTP app host/port
  settings remain reserved. Empty files clear local file overrides. Remote
  secret sync retains its nonempty-file and additive-update behavior.
- Key private local adapter/build directories by the effective environment's
  digest. Runner pools identify commands by executable/arguments and retain
  their original environment, so these paths select a pool with the new values.
  Recycle the previous pool and retire old definitions after requests drain.
  Synchronize publication of function process definitions and join the local
  watcher before shutdown cleanup.
- Diagnostics do not echo environment values or their digests.

## Consequences

Env-file edits reach ordinary HTTP apps and Node, Python, and Go functions at
their existing local URLs. Removed overrides fall back to lower layers. Local
cache directories can retain artifacts from earlier environment configurations.
