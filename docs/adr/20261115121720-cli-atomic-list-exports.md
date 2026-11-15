# ADR-20261115121720: Atomic local exports of paginated CLI lists

Status: accepted

## Context

Streaming invocation and build inventories makes large reads useful in pipelines,
but shell redirection exposes a partial destination if a later page fails. CI
and audit consumers need a completed local artifact and an integrity receipt.

## Decision

Add `--output PATH` to `invocation list` and `build list`, requiring `--all`.
The file always contains ordered JSON Lines; `--json` or `FAAS_JSON` selects the
completion receipt's format rather than the file format. `--stream` is optional
with a file sink; no-output behavior and default envelopes/arrays are unchanged.
An explicit empty output and raw `-` are refused; `./-` names a real file.

Validate the destination and existing parent before authentication/API reads.
Refuse existing files, directories and symlinks. Reuse the existing directory
pinning and user-prefix symlink policy; do not create missing parent directories.
Write each validated page to an exclusive random temporary file with mode 0600.
Retain one decoded page plus the existing bounded cursor history, including its
cycle and 1,000-page guards. Preserve all read filters, cursor precedence and
page-size validation. Human receipt mode does not fetch app names for file rows.

After traversal succeeds, recheck the directory identity, sync and close the
file, check cancellation, and link it into the pinned destination directory
without replacement. A racing destination causes failure without changing that
entry. Do not fall back to overwrite or buffered traversal if linking fails.
Cleanup removes only this operation's temporary name, on a best-effort basis.
File synchronization and atomic publication do not promise directory-entry
persistence across power loss or a point-in-time snapshot of a changing API.

A successful receipt contains `published`, `format`, `pages_completed`,
`items_emitted`, `bytes_written` and SHA-256 of the exact file bytes. Include the
absolute path only when UTF-8 and existing credential redaction leave it intact.
No cursors, filters, API addresses, credentials or exported rows enter receipts.
Human path rendering escapes controls. An empty result publishes an empty file.

SIGINT/SIGTERM cancel the active GET and retain the existing cancellation exit
130. Before publication, read/write/encoding/sync/close failures leave no final
file and produce no stdout rows. Preserve existing Problem classification and
API correlation. Private file bytes are not partial stdout, so errors do not
acquire streaming `pagination` metadata. Publication is the commit point:
subsequent receipt-write failure retains the completed file, and retry refuses
that existing destination rather than overwriting or automatically repeating it.
No server/SDK contracts or remote mutations are added.

## Evidence

Real-file core tests cover complete/empty exports, ordered pages, exact hashes,
private permissions, early visibility, cancellation, disk-full/short writes,
sync/close failure, traversal/encoding/cycle failure, cleanup and destination
races. Public CLI tests cover both commands, filters/cursors, receipt modes,
credential paths, refusal before requests, API correlation, directory races and
receipt failure. An isolated built-CLI journey covers completion, API errors,
SIGINT and a racing destination. Full hosted exact-head qualification remains
required; isolated core qualification is not full CLI/journey evidence.
