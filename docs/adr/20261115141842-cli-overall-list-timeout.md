# ADR-20261115141842: Overall read budgets for CLI lists

- Status: accepted
- Date: 2026-11-15

## Context

The global HTTP timeout bounds individual requests rather than a whole
paginated traversal. A large list/export can therefore outlive the budget a
script intended. Human invocation lists also fetch app names before rows.
Adding a budget must preserve existing output shapes, retry eligibility,
stream progress, cancellation and atomic file publication.

## Decision

Add optional positive --timeout DURATION to build list and invocation list.
Use the standard flag.Duration parser with last-value-wins behavior. Reject
explicit nonpositive values before client/authentication setup or private-file
creation; malformed/overflowing values retain ordinary flag-parser errors.
Omission leaves the original context and per-request defaults intact.

Create one invocation-local context after validation/output preflight/client
setup and before the first read. Use it for human app-name enrichment and all
page reads. Stream/file signal contexts derive from this parent, preserving
SIGINT/SIGTERM exit 130. Existing retry delays share the context, without
expanding the SDK's closed retry route set or changing attempt counts. Per-
request HTTP timeouts continue to apply and can expire sooner.

Check successful terminal reads against the context before buffered output.
Check a completed stream against it too, retaining fully emitted page/item
counts and partial=true when any rows were written. Preserve actual API/output
errors rather than replacing them with the deadline, including API codes and
request IDs. Timeout remains the existing temporary-failure exit 3. Private
export bytes never acquire stdout pagination metadata.

Keep the publisher's existing context checks before sync and after close.
Expiry before publication leaves no final artifact; cleanup owns only its
private temporary name. Link remains the commit point: subsequent expiry or
receipt failure retains the completed file. This is a cooperative read budget,
not an interrupt for blocking local I/O or a strict process wall-clock cap.
No server work is submitted, canceled or replayed by these read-only commands.

Register the option in the existing manifest and regenerate only the two
reference sections through the production Markdown functions. Keep JSON help
output contracts and flag rules, human defaults and other command timeouts.

## Verification and consequences

A stdlib-only race suite uses the actual context helper and publisher to test
positive/omitted budgets, earlier parent deadlines, cancellation, preservation
of real failures and expiry during sync before publication. Public CLI tests
cover live HTTP bodies across page/all/stream/file modes, invalid values before
login, human enrichment and its retry wait, API correlation with retries
configured, successful output/last-wins, discovery and terminal-stream progress.
A built-CLI isolated journey covers active reads and signal cancellation with
a selected timeout. Full hosted exact-head checks remain required: the local
core pass does not qualify CLI dispatch, actual SDK retries or that journey.
