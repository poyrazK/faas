# ADR-20261121213156: Recover a Task handle after a lost submission reply

Date: 2026-11-21
Status: Accepted

## Context

An MCP client can reconnect to a durable Task once it has the Task ID. A
connection can also fail after the database commits the Task and before the
client receives that ID. An agent needs a way to repeat the submission safely.

## Decision

- The Node and Go PostgreSQL starters advertise `io.gregale/task-submission` on
  authenticated modern endpoints after detecting the additive Task migration.
  The client supplies a saved UUIDv7 as `io.gregale/task-submission-key` in the
  `tools/call` metadata. The keyed `Mcp-Method` marker makes older starter
  replicas reject a mismatched method header. The gateway and guest authorize
  the JSON-RPC body as a tool call. A supporting guest validates the marker
  before dispatch.
- The existing PostgreSQL admission transaction commits the key and Task
  together. A partial unique index scopes keys to the Task namespace. A replay
  requires the original authenticated caller, tool, validated arguments and
  input capabilities. The existing encrypted arguments provide the comparison.
- PostgreSQL accepts keys for one minute from their UUIDv7 timestamp. The
  minimum Task lifetime covers that whole window, so normal Task cleanup can
  remove the key with the Task. A replay preserves the original task ID and
  deadline. Worker retries retain their at-least-once execution contract.
- The Go web runtime discovers the shared additive index at startup. Ordinary
  Tasks continue working on a schema awaiting migration.
- The `io.gregale/task-submission-lookup` extension allows a read-only
  `gregale/tasks/lookup` request for a retained Task even after the key's
  submission window closes. It filters on namespace, owner and expiry, then
  checks current tool access. It reads only metadata columns and uses the
  original Task deadline. The CLI reports the Task ID and next action.

The Python/PostgreSQL Tasks starter implements the same submission and lookup
contracts. Its schema-owner command installs the shared UUID column and partial
unique index. Older schemas continue to serve ordinary Tasks during rollout.
