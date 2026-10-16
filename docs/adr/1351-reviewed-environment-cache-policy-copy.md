# ADR-1351: Reviewed environment cache-policy copying

Status: Accepted

## Context

Environment cache policies support guarded replacements, inheritance restoration,
transactional receipts and gateway convergence (ADRs 1324–1326). Agents need to
promote an explicitly reviewed policy between named environments without relying
on separate, racy source inspection and destination mutation.

## Decision

Support one-time copies between distinct named environments of the same app and
project. Add optional `copy_from` (scope, immutable environment UUID, positive
revision) to the existing native PUT. Keep explicit normalized rules in the
request and require equality with the pinned non-inherited source in the same
write transaction. Empty explicit sources copy cache-disabled mode; inherited
sources are unsupported. Cross-app/project copies and ongoing synchronization
are outside this operation.

Reuse the existing account/app lock and environment identity locks for both
source and destination. All app policy writes serialize on the app before
locking environments, including opposite-direction copies. Validate source and
destination immutable identity before receipt recovery. Check revisions and
source rule equality only before a new write: the original receipt must remain
recoverable after later edits. Include the source pin in the request hash;
omit nil pins to preserve existing request hashes. No additional tables or SQL
are required. Native account authorization remains the API authority boundary.

Add separate MCP copy plan/apply tools. Planning requires source and destination
read consent and displays both snapshots and the exact proposed destination
request. Applying requires source read consent and destination write consent;
source write consent is unnecessary. Check both identities and current consent
before returning even a cached receipt. Bind the copy action, snapshots, source
pin and destination guard into the review and encrypted durable journal. Reuse
15-minute review expiry and atomic native retry receipts. Distinct apply tools
cannot consume other action plans. Revision races fail closed at the native
transaction even if a source changes after MCP inspection.

The copied destination is an ordinary independent explicit policy revision.
Existing gateway reload, environment-specific response IDs and current-session
convergence observations apply without a new gateway protocol. Watch the exact
returned destination revision; the receipt is desired intent, not serving proof.

## Consequences

Agents can review and promote explicit cache policy without source mutation,
revision races or ambiguous retry duplication. Native copying requires updated
apid; existing environment-cache migrations and compatible gateways remain
prerequisites. Inherited rules are not exposed or frozen; inheritance restoration
remains a separate operation. No new migration is needed for copy metadata,
which lives in the hashed request, review and existing receipt journal.
