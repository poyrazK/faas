# ADR-20261119000001 Reviewer inbox for pipeline clarifications

The Node and Python SDKs expose pending clarification assignments across
business records through a read-only MCP list tool. PostgreSQL scans live
grants using the reviewer binding and a stable position derived from the grant
ID. The cursor is signed for that reviewer and scope. The runtime checks each
candidate against current gate authorization and rereads its grant before
returning a summary. A bounded scan can return a partial or empty page with
a continuation cursor.

The typed watch reuses the work inbox refresh lifecycle. The MCP list is
the source for the initial, periodic, and reconnect reads. Applications can
attach a private notification source for earlier refreshes. The watch
coalesces repeated hints and emits bounded replacement snapshots. The existing
handoff read and reply tools remain the path to the form and its audited
decision.
