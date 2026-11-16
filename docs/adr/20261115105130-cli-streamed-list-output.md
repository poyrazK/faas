# ADR-20261115105130: Opt-in streamed list output

Date: 2026-11-15

Status: accepted

## Context

Cursor lists buffer the whole traversal to avoid partial default output.
Large inventories therefore delay pipelines and retain all rows. Invocation
and build lists need an explicit streaming contract for incremental readers.

## Decision

Add --stream to invocation list and build list, requiring --json and --all.
Invalid combinations fail before authentication. Existing page sizes, filters,
starting cursor, alias precedence and default output shapes remain intact.
The opt-in path emits one JSON object per row as each validated page arrives;
an empty result writes no lines. It retains one decoded page and the existing
bounded cursor history, with the unchanged 1,000-page/cycle guards. Default
collection buffers through the same page walker and emits nothing on traversal failure.

The stream owns a signal context for SIGINT/SIGTERM. Cancellation stops the
active read and returns the existing cancellation exit 130. Output/encoding
failures stop paging immediately. Normal OS SIGPIPE behavior still applies
when the consumer closes a real stdout pipe.

An opted-in stream error retains the existing Problem code, category, exit and
request ID, adding a CLI-local pagination object to stderr. pages_completed
counts fully emitted pages (including empty pages); items_emitted counts
complete JSON records. partial is true when any stdout bytes were written
before failure, including an incomplete first record. A nonzero exit means the
stream is incomplete. There is no terminal stdout envelope or automatic replay.
No cursor, row, filter, profile, URL or credential enters this progress metadata.

The decorator starts with the existing Problem writer's complete envelope and
uses json.RawMessage for each field, preserving the existing field values and
numeric precision. Other errors retain their exact writer path. No API/SDK
wire contract changes. All requests remain read-only.

## Validation

Core tests exercise early/ordered rows, empty pages, API callback failure,
cycles/budgets, last allowed page, cancellation and output/encoding failures.
Existing collector guards retain their all-or-nothing contract. Public CLI
tests cover filters/cursors, old shapes, option refusal, partial API errors and
correlation, delivery before the next page completes and broken-pipe refusal
of further reads. A built-CLI journey covers early rows, API failure and SIGINT
of an active HTTP read. Local and hosted evidence is recorded separately.
