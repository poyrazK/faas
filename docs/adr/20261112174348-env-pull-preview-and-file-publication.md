# ADR-20261112174348: Preview env-pull changes and publish complete local files

Status: accepted

## Context

`env pull` creates a key-only skeleton from sealed-secret metadata. It preserves
existing local values but provides no preview, rewrites files on no-op pulls,
and writes the destination directly. Users cannot inspect missing keys without
editing a file, and interruption can leave a partly written destination.

## Decision

Add `env pull --dry-run` with text and JSON output. One shared plan classifies
each recognized key as `add`, `preserve`, or `local_only`; only missing remote
keys are appended as empty assignments. Keep original bytes, comments, values
and line endings intact. Recognize the same `export` prefix supported by push.
Append missing keys in sorted order, preserving CRLF style when present.
Validate remote key names before using them as file content. Reports contain
key names and decisions, never local values, remote hashes or secret values.

Capture the destination before requesting remote metadata. Preview performs
only remote reads and never stages or writes output. Apply skips an existing
file when no keys are missing. Retain creation of an empty skeleton for a
missing destination with an empty remote scope. Existing JSON receipt fields
remain, with additive plan, preview and write-result fields.

Reject symlink and non-regular destinations before contacting the API. Stage a
complete `0600` file in the destination directory, sync and close it, then
recheck the captured file identity, size, mode, modification time and contents.
Refuse observed changes instead of overwriting a concurrent edit. Publish a
new path using a link without replacement, and replace an existing regular
file by rename. There is no fallback to truncating the live file. Clean up
temporary files on returned failures and cancellation. Filesystems that do not
support the publication operation fail without replacing the destination.

The final recheck is optimistic detection, not a lock on external editors;
another process can still change an existing destination between recheck and
rename. File synchronization is not a promise of directory-entry persistence
across power loss. An abruptly killed process may leave a private temporary
file, but does not expose a partly written live file.

## Consequences

Users can review exact key decisions before changing a local file, and no-op
pulls stop triggering file watchers. Apply retains customer bytes and avoids
partial writes. Tests cover preview/apply agreement, CRLF and export lines,
zero output writes, no-op identity, concurrent edits and staged failure paths.
The environment lifecycle journey covers preview against real app metadata.
