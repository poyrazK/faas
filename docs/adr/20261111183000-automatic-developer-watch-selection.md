# ADR-20261111183000: Automatically select supported developer watch mode

Date: 2026-11-11
Status: Accepted
Amends: [ADR-1170](1170-developer-watch-mode.md), its opt-in default and
rejected automatic-selection alternative

## Context

Remote developer environments already support framework development commands,
live patches without restarting the development server, and background build
convergence. The CLI still requires developers to discover `--watch` before
using that path. Explicit disabling must remain reliable in mixed workspaces.

## Decision

- Resolve the remote mode at loop startup: explicit CLI flags, then
  `dev.watch`, then a detected development command, then the existing build or
  function-runner path. Automatic candidates are Node.js/Python apps without
  a conventional or manifest-selected Dockerfile. Go and functions retain
  their current runtime path. `--watch=false --watch-command ...` is a conflict.
- Reuse the existing watch API and runtime gates. An automatic request receiving
  HTTP 422 `dev_watch_unsupported` can resubmit the same session target and
  resources with watch off. This rejection precedes session mutation. Other
  errors, including transport/5xx and unknown outcomes, do not cause a fallback
  submission. RAM and authentication are never changed to make watch work.
- A successful older-server response reporting watch off selects the reported
  build mode for automatic choices. Explicit choices require a matching watch
  response. Mismatched settings fail before any source upload.
- Print the selected mode, command, source and fallback explanation. Emit a
  `developer_mode` NDJSON event before source sync for JSON consumers. Setup
  receipts expose a local plan under `development`; server eligibility is
  checked only when starting. Commands do not enter sync-history telemetry.
- In `--all`, unset mode flags let every child resolve its own defaults.
  Explicit false is forwarded as `--watch=false`, overriding each child's
  manifest. Existing `--all --watch` behavior still requests watch for members
  with detected development commands; other members resolve their own settings.
- Prepare framework host/origin configuration only after a watch session is
  accepted, so an automatic fallback does not request irrelevant source edits.

## Consequences

Supported apps get the existing fast edit path from plain `gregale dev`.
Developers can opt out for production-shaped testing, and every selected mode
is visible. Existing builders, guest behavior, resource limits, build isolation,
and snapshot guards are unchanged. Explicit watch choices remain strict.
Mode settings are captured at loop startup; restart to apply a new choice.
