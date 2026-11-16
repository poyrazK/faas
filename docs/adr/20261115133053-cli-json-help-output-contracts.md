# ADR-20261115133053: Offline output contracts in CLI JSON help

- Status: accepted
- Date: 2026-11-15

## Context

Version-1 JSON help describes arguments and options but cannot tell an offline
consumer whether a list returns one page, a collected array/envelope, JSON Lines
or a file-export receipt. Guessing from flag descriptions can select the wrong
parser or discard a completed file after a receipt-write error.

## Decision

Add an optional `output` object to the existing JSON help document. Register
explicit typed contracts only on the build-list and invocation-list manifest
leaves, preserving canonical and compatibility lookup and the filtered flag
inventory. Do not infer output from prose or promise schemas for API rows.
Unregistered commands omit the object and schema_version remains 1.

The scope is effective JSON mode on valid invocations. Each mode has Boolean
`when` conditions: explicit false is meaningful, an omitted key is unconstrained,
and `output` means flag presence, never a supplied path. Filter unavailable
selectors and omit modes requiring unavailable flags. File mode leaves stream
unconstrained because redundant streaming is accepted with exports.

Describe JSON page objects, collected build arrays/invocation envelopes,
per-page JSON Lines, and file completion receipts with their empty behavior.
File metadata describes private JSON Lines staging, atomic no-overwrite
publication, no final artifact on a pre-publication failure, and retention of
the completed artifact after a receipt error. It makes no directory-fsync or
API snapshot-consistency claim. Receipt path remains optional.

Error metadata identifies stderr Problems, optional request IDs and the shared
status-to-exit mapping. Derive HTTP exit values from the existing production
mapper. Read failures and stdout-write failures have distinct policies:
buffered reads discard rows, streaming can retain earlier records and optional
pagination progress, while any stdout encoding/write can tear its output.
Stream/file signal cancellation retains exit 130; native OS signals can terminate
without a Problem. File errors route the underlying error without falsely
reporting private file bytes as partial stdout.

Construct fresh owned metadata from static registered words and filtered flag
names. Discovery performs no API calls and includes no defaults, argv, option
values, paths, profiles, credentials or row data. Human help, parser behavior,
list operations, SDK/server contracts and generated human references are unchanged.

## Verification and consequences

Core tests cover selector partitions, hidden options, owned collections,
receipt-field identity and the actual HTTP exit mapper. Public CLI tests compare
the published contracts with real mock-API stdout, empty inventories, API errors,
request IDs, partial output and retained artifacts after receipt failure. An
isolated built-CLI journey discovers contracts offline and uses them to parse
both commands' actual output. Full hosted checks must qualify the published
head; a stdlib-only core suite does not qualify CLI dispatch or the journey.

Consumers can choose a parser without contacting a server, but must still
validate option combinations, handle stderr/exit status and tolerate optional
fields. Adding another command requires explicit registration and corresponding
runtime contract coverage rather than assuming uniform CLI output.
