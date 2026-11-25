# ADR: Look up a retained pipeline-tool start

Status: Accepted (SDK preview)

## Context

A pipeline tool reserves a durable receipt before dispatch. If its native
start response is lost, the receipt remains `start_unknown`. Existing
reconciliation requires a verified native run ID.

## Decision

Expose an account-app read route at
`GET /v1/apps/{slug}/workflows/run-starts/{name}`. Require the original
pipeline tool's `Idempotency-Key` header. The route checks the app's current
read access, looks up one retained non-tenant binding in the existing
workflow run key index, and returns only the run ID. The workflow's current
published definition is irrelevant to lookup of a retained admission.

The SDK first authorizes receipt access as reconciliation. It verifies the
native app, workflow, owner, contract, trigger identity, and input digest,
then checks application authority again. An unresolved receipt can surface
its existing reconciliation tool through the scoped inbox follow-up. The
caller invokes reconciliation separately and the receipt store binds every
concurrent adoption to the same checkpoint.

## Evidence

Native routing tests cover scoped lookup and retention of one start.
Shared SDK cases cover a lost response, missing lookup evidence, the same
business input under another invocation, and concurrent adoption. The CSV
MCP journey drops the start response and resolves the original run from
the import's inbox.
