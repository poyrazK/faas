# 1104: Message TTL for scheduled realtime publication

## Decision

Schedules accept immutable `ttl_seconds` from zero through 86400. Zero preserves
existing delivery behavior. Each successful occurrence starts its lifetime at
its retained message creation timestamp, rather than schedule creation or its
planned delivery time. Failed attempts and intentionally skipped occurrences
create no message and consume no message lifetime. Recurring occurrences receive
independent deadlines.

Persist the TTL on the schedule, include it in creation identity and publication
candidate checks, and pass it through the existing atomic retained append. The
message and its expiration companion row commit together with schedule progress.
Publication retries cannot republish a committed occurrence or extend its deadline.
Legacy fan-out carries the committed deadline. Existing expired-message checkpoints
advance retained WebSocket and SSE cursors; REST history suppresses expired payloads.
Expiry does not remove history positions or reverse reducer or consumer-group work.

Apply migration 20261012102700544 after the message-expiration migration and before
updated apid workers. Deploy checkpoint-capable clients before opting into TTL.
Schedule rescheduling, pausing, retrying and group actions preserve the immutable
TTL. The request, schedule response, OpenAPI and Python/Node models expose it.

## Validation

Static Go build, SDK type/lint/compilation and whitespace checks only. Runtime
verification and migration application remain deferred at the user's request.
