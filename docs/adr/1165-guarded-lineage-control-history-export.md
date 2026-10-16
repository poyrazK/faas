# ADR-1165: Guarded lineage control-history export

Status: Accepted
Date: 2026-10-13
Related: ADR-1161, ADR-1163, ADR-1164

## Decision

Add a read-only CLI control-history export for incident reviews. Require the app,
subscription, job UUID, reviewed original created_at and a new destination path.
Resolve and validate the retained job identity, then read the entire unfiltered
journal once with after=0 and the maximum page size. The 128-receipt per-job bound
fits in that one GET. Apply optional exact action/actor/credential filters locally
after validating every receipt; this avoids page races and dependence on newly
added server filter support during rolling upgrades.

Reject paginated or oversized responses, missing/duplicate command identities,
noncontiguous original sequences, cross-scope or incarnation/retention mismatch,
and malformed original outcomes or actor metadata. Preserve legacy receipts with
all actor fields absent, without inferring an actor. Capture the unfiltered
observed receipt count and through_sequence even if the selected set is empty.
Later commands are outside this read; timestamps are informational local clocks,
not an ordering guarantee or server-issued snapshot token.

Write version 1 of gregale.event-lineage-control-history as JSON, with app slug and
stable app/subscription/job IDs, creation guard, original expiry, observation time,
normalized selected filters, observed count, sequence boundary and selected
immutable receipt metadata. Do not include job selections, case results, event
payloads or credentials. Empty histories and empty filtered selections are valid
artifacts. The file is evidence, not a replay request, and is unsigned/editable.
It remains a local observation after server retention expires, not a complete
historical audit or reservation of future commands.

Bound the complete encoded artifact to 256 KiB in pkg/api/limits.go. Write it to a
private 0600 temporary file in the destination directory, sync and close, then
publish its completed inode with a no-replace hard link. Existing files and
symlinks are never overwritten; failure cleans the temporary file, and the final
path never exposes partial contents. A process crash may leave a private temporary
file, but never a partial final artifact. Filesystems without hard-link support
fail rather than fall back to partial publication. Syncing file contents does not
promise crash durability of the destination directory entry.

Use a positive bounded overall timeout (default two minutes), existing per-read
control timeouts and Ctrl-C cancellation checks before publication. A successfully
published artifact stays committed. JSON stdout is a summary of the saved file,
not the receipt bundle. No new API route, SDK operation, database migration or
compliance registry entry is needed.
