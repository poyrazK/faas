# ADR-1028: Realtime event inspector

Status: operator preview

## Context

Retained history, transactional outboxes and channel webhooks exist, but developers
need a bounded dashboard view of events and delivery failures. Customer outbox
rows reside outside the platform database and cannot be read directly by the UI.

## Decision

Add a server-rendered app realtime inspector using the existing dashboard session,
app authorization, realtime plan and history preview gates. List registered
endpoints and bounded channel heads, page up to 50 retained events, and expose
payload/metadata through escaped details elements. Responses are no-store and
use no-referrer. No payload enters audit metadata or query parameters.

The existing managed relay mirrors a payload-free sample of up to 32 unpublished
realtime outbox rows per qualified source, blocked first. Source account ownership
and credential revision fence observation updates. Replace samples atomically,
and cascade them on source deletion. Failed observations leave previous rows
with their original timestamps; an empty sample is never delivery evidence.
Source health counts remain combined ordinary Commit/realtime aggregates. The UI
filters the sample to the selected endpoint/channel without using sealed source
credentials or querying customer databases. In-memory stores support retained
inspection; source observations require the PostgreSQL managed Commit store.

The inspector reads the latest 20 deliveries of one selected webhook, filters
channel-event payload routing fields, and links to existing attempt inspection and
CSRF-protected dead-delivery retry controls. It adds no mutation path. Full event
payloads remain accessible only through existing app-scoped retained history.

## Consequences

The relay adds one bounded source read and one platform observation transaction
per qualified realtime tick. Apply the observation migration before enabling this
version of the transactional preview. Source samples can become stale and are not
an exhaustive outbox ledger. Customer database helpers remain authoritative for
individual pending/blocked status, repair and retry. Event history retention gaps
require explicit snapshot resync; browsing the retained floor does not repair a
client cursor. There is no new REST API, SDK operation, JavaScript dependency or
pricing tier. Runtime tests and migration activation remain deferred.
