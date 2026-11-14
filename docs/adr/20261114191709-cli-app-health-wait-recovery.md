# ADR-20261114191709: Read-only recovery for CLI app health waits

Status: Accepted
Date: 2026-11-14
Related: UX spec §3.2; ADR-20261114164404 (Workflow wait recovery)

## Decision

Extend the existing `gregale app health APP --wait` JSON Problem with optional
`recovery.app`, `recovery.environment`, `recovery.inspect_command` and
`recovery.resume_command`. Use the app resolved by the existing dispatcher,
including a linked app. Keep the selected profile and explicitly selected
environment. Omit `environment` and the environment flag when the original
read used the default-scope route. The reserved scope name is not a project
environment. Inspection uses `app health --app APP`; continued waiting adds `--wait` with the
original timeout and polling interval. Use the existing POSIX argument quoting
and credential redaction. Invalid targets do not invent recovery commands;
long preview and readable legacy app names use the existing CLI name rules.

Recovery only reads recorded health evidence. It does not wake, restart,
redeploy or probe an app. A recovery target does not prove the app exists or
that its health is known. Callers retain endpoint and credential overrides;
commands do not embed either. Each resumed wait gets its original duration as
a new local budget, rather than reusing an expired absolute deadline.

Keep the health assessment DTO on stdout. Timeout retains its last observation
as before; JSON permanent read failures and interruption now retain it too.
No observation means no fabricated assessment. Retained assessments keep their
original scope and timestamps and can be stale or unhealthy. A snapshot write
failure takes diagnostic priority. Human output remains unchanged.

Use the shared wait Problem writer. Preserve server Problems, validated request
IDs, credential redaction and HTTP-to-exit mapping. Preserve local timeout's
existing Problem and exit 124, and interruption's transport Problem and exit
130. Local stops do not reuse correlation from a transient read. Preserve the
existing retry policy for HTTP 408/429, server, network and malformed-response
errors. Success still requires healthy status, the requested scope, a positive
validity window and a nonfuture, unexpired assessment timestamp.

## Verification

Public CLI tests cover linked/default/explicit/preview targets, permanent error
exits, exact last assessments, absence of observations, correlation and
redaction, freshness/scope guards, retry behavior and snapshot write failures.
Real SIGINT/SIGTERM subprocesses must retain exit 130, cancel an active HTTP
read and preserve any prior assessment. A built-CLI journey executes emitted
inspection and wait commands from another directory after timeout, API refusal
and interruption, allowing only GETs for the original app and health scope.
