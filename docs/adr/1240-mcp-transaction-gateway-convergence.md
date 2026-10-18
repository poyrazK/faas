# ADR-1240: Check MCP gateway convergence during traffic admission

Status: Accepted

## Context

Account-scoped CLI audits can observe fresh gateway replay, but gateway restarts,
membership changes, and unrelated account policy mutations can invalidate that
observation before traffic promotion. Direct API callers need the same condition.

## Decision

Extend `expected_mcp_access_policy` with optional positive `account_revision`.
After acquiring the existing account policy commit fence and comparing the stored
policy fingerprint, the traffic transaction reads account revision and all active
serving gateway edge-rule reports in one PostgreSQL statement snapshot. The
revision must equal the audited value, at least one gateway must exist, and every
report must be at most ten seconds old and cover the revision. Missing reports
and future timestamps fail closed. The unknown-owner historical revision floor
continues to apply.

Return distinct HTTP 409 codes for changed revision, absent fleet, stale evidence,
and pending replay. Legacy fingerprint-only and unguarded calls retain their
existing behavior. In-memory stores cannot produce durable gateway evidence and
reject the new condition. CLI audit output carries the successful revision into
both promotion and recovery. Explicit condition failures cannot be accepted as
lost responses based on a later serving-deployment read.

## Consequences

The admission check covers changes committed between CLI verification and the
server snapshot. It does not lock fleet membership or heartbeat writes, guarantee
future gateway health, or attest OAuth token enforcement. A concurrent gateway
change after the snapshot can occur after the admission decision. Avoiding global
registry locks keeps heartbeat and membership updates independent of customer
traffic transactions. Policy commits remain fenced through traffic commit.
