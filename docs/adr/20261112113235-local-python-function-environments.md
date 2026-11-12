# ADR-20261112113235: Resolve local Python functions in their project environment

Date: 2026-11-12
Status: Accepted
Amends: ADR-20261107210000 (local functions)
Related: ADR-20261111162000 (local environment reload)

## Context

Local HTTP apps put project virtualenv tools on PATH, but local Python functions
resolve `python3` using the CLI process's PATH. A project installed with a virtual
environment can therefore fail to import its dependencies. Selected env files
and per-function PATH overrides also do not influence interpreter selection.

## Decision

- Resolve Python functions through an existing `.venv` or `venv` interpreter
  in the function root first, then in the selected workspace root. Do not search
  ancestor directories beyond that workspace. An existing unusable interpreter
  produces an actionable error instead of silently selecting a different one.
- When no project interpreter exists, use the function's effective PATH after
  shell, resource, selected env-file and function environment layering. The last
  PATH assignment wins. Preserve Go's guard against executables found through a
  relative PATH entry.
- Preserve the virtualenv executable path, including symlinks, so Python finds
  `pyvenv.cfg` and its site-packages. Prepend its tool directory to the child
  PATH, set VIRTUAL_ENV and clear PYTHONHOME, matching virtualenv activation for
  imports and subprocesses. No dependency installation or shell activation runs.
- Resolve before choosing the private adapter directory. Environment changes
  keep the existing digest-based worker identity and reload behavior; an invalid
  runtime edit leaves the current worker available and a repaired edit retries.
- Keep interpreter selection local to the Python function. Do not mutate the
  CLI process environment or print environment values.

## Consequences

Single-function projects and functions projects can share workspace dependencies
or use a function-specific virtualenv. Local development can use those packages
without activating a shell. Deployment still installs dependencies from declared
project inputs; this does not change the deployed Python runtime or runner ABI.
