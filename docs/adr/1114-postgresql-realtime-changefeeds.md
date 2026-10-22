# ADR-1114: PostgreSQL realtime table-change feeds

Status: operator preview

## Context

Transactional realtime publishing requires every business writer to insert an
outbox event. Applications want automatic capture of selected table changes.

## Decision

Provide explicitly installed customer PostgreSQL trigger functions, reusing
ADR-1112's transactional realtime outbox and managed relay. The owner binds one
named feed to an ordinary table with a primary key, an endpoint, an explicit
column allowlist, and a static channel or tenant channel prefix. Primary key and
tenant fields must appear in that allowlist. Trigger execution uses the writer's
privileges; no SECURITY DEFINER or additional platform database access is added.

Capture INSERT, UPDATE and DELETE after each row operation. Updates that do not
change selected fields produce no event. Tenant moves are rejected; callers must
explicitly delete then insert to notify both tenant channels. Events contain feed
name, operation, key, and selected before/after values. Table schema/name and SQL
are not sent to subscribers. Owner installation/removal performs identifier-quoted
DDL in the customer database, an explicit exception to the platform's static SQL
convention; SDK calls remain parameterized. The relay performs no DDL.

## Consequences

Rollback removes the queued event. Outbox constraints, permissions or invalid
routing cause the business write to fail rather than silently omit capture.
Existing bounded frames apply to the entire JSON envelope including both row
images. These are change notifications, not WAL CDC, snapshots or exactly-once
client processing. TRUNCATE, partitioned tables, historical backfill and schema
migration automation are excluded. Changes to bindings require transactional
remove/install. Database owner roles can disable triggers; this remains a trusted
customer database boundary. Subscribers still require application authorization;
tenant routing alone grants no access. No additional daemon or preview gate is
needed beyond transactional realtime publishing.
