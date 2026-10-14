# ADR 1049: Delayed publishing visibility in the realtime inspector

Status: accepted for preview implementation; runtime/UI validation deferred.

## Decision

Reuse the existing retained schedule engine for delayed publishing. It already
supports immutable schedule IDs for idempotent creation, version-checked
rescheduling/cancellation, bounded capacity, retry/recurrence and release-time
retained publication. Add inspector observation rather than another queue or API.

Show the selected channel's full bounded schedule list, statuses and pending,
paused/failed totals, UTC delivery/next-attempt times, versions, attempts, group,
recurrence, last published sequence and diagnostic error. Exclude payloads.
Use a separate schedule error so schedule loading failures do not hide history.
Existing dashboard ownership, plan and retained-preview gates apply.

Channel discovery unions channel heads with schedules that are pending/paused or
have a terminal update within 24 hours. Use sqlc for the PostgreSQL discovery query
and the same deduplicated, sorted 32-channel bound in memory. Schedule-only
channels have empty history (oldest 1, latest 0); discovery does not allocate
sequences or modify channel state. Users can enter any channel explicitly when
it falls outside the discovery sample. The existing 256-schedule endpoint bound
limits inspector lists; terminal expiry/cancellation races retain existing semantics.

No new migrations, public API, SDK generation, scheduler or fanout path. Existing
SDK schedule operations/helpers remain the integration surface. UI rendering and
runtime checks are deferred at the user's request; builds and static generation
checks alone do not validate Postgres execution or template rendering.
