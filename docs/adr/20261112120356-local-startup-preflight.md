# ADR-20261112120356: Validate local execution tools before resource attachment

Date: 2026-11-12
Status: Accepted
Amends: ADR-20261107210000 (local development)
Related: ADR-20261112113235 (Python function environments)

## Context

Local development attaches remote databases and buckets, or starts offline Docker
resources, before preparing the selected workload. A missing interpreter or
framework tool can therefore be discovered after starting a task VM, minting a
credential, or wiping offline data with `--reset`.

## Decision

- After parsing the selected env file and planning the workload, validate every
  function's runtime and detected HTTP app tools before any resource attachment,
  Docker invocation, reset, cache creation or server startup.
- Use the effective shell, selected env-file and function environment when
  resolving tools. Reuse Python's function/workspace virtualenv selection. Node
  and Go function preparation must use that same PATH and Go builds receive the
  selected environment. Resource bindings are injected afterward as before.
- For detected Node apps, check the runtime, selected package manager and plain
  known framework launchers. Respect project and ancestor workspace binaries.
  Package scripts using Yarn PnP do not require `node_modules/.bin`.
- For detected Python development servers, use bounded stdlib module discovery
  in the selected interpreter for the framework and server modules. Do not import
  application or framework code. Discard probe output because site customization
  can print configuration values.
- Report the affected function/tool or module and an appropriate repair command
  for the project's lockfile. Never print environment values, install packages,
  run customer scripts, build the app or contact a package registry in preflight.
- Preserve custom commands, script wrappers, shell composition and preparatory
  scripts conservatively; their runtime requirements remain the supervisor's
  responsibility. Check shell availability for every HTTP app. This is an
  execution-prerequisite check, not proof that arbitrary application code starts
  successfully or that all dependencies are installed.

## Consequences

A missing tool fails promptly without changing offline data or borrowing remote
resources. Existing startup and reload validation still handles source/build
errors and edits after preflight. No cloud API, deployment runtime, billing or
resource provisioning contract changes.
