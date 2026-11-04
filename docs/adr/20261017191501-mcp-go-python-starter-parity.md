# ADR-20261017191501: Browser and Task migration parity for Go/Python MCP starters

## Status

Accepted; refines ADR-426 and the built-in starter capability contract.

## Context

Go and Python rejected untrusted browser origins but did not answer CORS
preflight or consistently expose protocol and authorization response headers.
Their Task startup also performed schema DDL with the service database account,
preventing deployments with data-only service roles.

## Decision

Support MCP endpoint OPTIONS preflight for exact allowlisted origins before
bearer authentication. Bound allowed methods and headers to the existing Node
starter vocabulary; reject duplicate origins, unsupported methods and headers.
Expose WWW-Authenticate, MCP-Protocol-Version and Streaming-Status on responses.
Actual requests retain all bearer, scope and catalog-readiness checks.

Move Go/Python Task DDL into explicit apply/status commands requiring
MCP_TASK_MIGRATION_DATABASE_URL. Do not fall back to runtime credentials or start
service loops from these commands. Apply runs transactionally with an advisory
lock, requires a trusted schema without PUBLIC CREATE, preserves existing data
and installs a shared portable-starter version marker. Refuse future versions
before changing existing schema. Keep Node's separate schema/admission contract.

Web, worker and observer startup read the installed version, required columns
and enabled notification trigger. Runtime accounts need data grants and SELECT
on the marker, not schema CREATE or table ownership. Observer read-only checks
continue to apply.

Go and Python enforce the shared namespace and owner running limits with the
same execution advisory lock as Node; expired leases cannot be renewed. They
honor persisted retry due times and configured attempt budgets. Handler errors
remain terminal unless Go returns an error wrapping `ErrRetryableMCPTask` or
Python raises `RetryableTaskError`. Migration apply adds `next_attempt_at`
without replacing existing rows; startup checks require that column.

All starters hash the same canonical JSON array of resource, subject and client
ID for new Tasks. Read and caller-write authorization also accepts the earlier
Go identity with duplicated JSON quotes, preserving retained work and caller
isolation. Outstanding admission counts both forms for the caller.

## Consequences

Existing Go/Python queues must run apply before upgrading these starters.
Upgrade all workers before relying on shared capacity and retry enforcement.
Bind migration credentials only to an operator process and use a dedicated
schema with the same search_path for all accounts. Browser origin permission
never grants bearer authorization. Portable contract checks do not replace
native runtime or real OAuth/provider release qualification.
