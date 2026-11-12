# ADR-20261112100408: Let local framework servers own source reload

Date: 2026-11-12
Status: Accepted
Amends: ADR-20261111140000 (local HTTP apps)
Related: ADR-1170 (framework development commands), ADR-20261111162000 (local env reload)

## Context

The local HTTP app supervisor restarts its process group after every source
edit. Native framework watchers already handle those edits. Killing them
disconnects HMR WebSockets and loses browser development state on each save.
Package-manager dev scripts also hide the underlying framework from local
host/port selection, so a Vite script can listen on a different backend port.

## Decision

- Carry an explicit HMR capability alongside shared development-command
  detection. Recognise Next.js, Vite, SvelteKit, Astro, Nuxt, React Router and
  Vite-based Remix commands, including simple direct package.json dev scripts
  with their required framework dependencies declared. Preserve shared command
  selection and messages for remote watch mode.
- Keep opaque scripts, shell wrappers/chains, explicit commands, Python servers
  and production start commands under Gregale source-restart supervision.
  A declared framework dependency alone does not give an arbitrary script HMR
  ownership.
- Bind recognised local commands to the reserved loopback backend. Forward
  listening flags through package managers, including npm's argument separator.
  Dependencies remain installed by the developer, never downloaded by Gregale.
- While a framework process is running, leave ordinary source edits to its
  watcher without changing proxy readiness or its WebSocket connections.
  Separately fingerprint the selected environment file, Gregale manifests,
  package.json, root package-manager configuration and dependency lockfiles.
  Watch these launch inputs even when ignored; settle and validate them before
  replacing the process. Framework-specific configuration stays with its watcher.
- Re-resolve the command and reload ownership after each launch-input change.
  Invalid configuration keeps the current process. A stopped or crashed server
  still recovers on the next source save regardless of reload ownership.
  `--no-reload` suppresses Gregale automatic restarts while leaving a framework's
  own reload behavior available.
- Retain stable local URLs, process-group cleanup, environment layering and
  function reload behavior. Never print environment values or fingerprints.

## Consequences

Supported local framework apps preserve their development process and HMR
connections across ordinary edits. Launch-input changes still replace that
process. Conservative script recognition leaves complex commands with full
Gregale restarts. Local process ownership remains Unix-only; no runtime,
deployment, quota or VM lifecycle contract changes.
