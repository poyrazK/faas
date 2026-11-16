# ADR-20261115135218: Discoverable CLI list flag dependencies

- Status: accepted
- Date: 2026-11-15

## Context

List output contracts explain how valid commands serialize results, but an
offline caller still needs flag descriptions or failed attempts to learn which
combinations are valid. Existing stdout-stream and file-output checks also use
different Problem codes. Adding advice must not normalize those stable codes,
change human wording or move validation past authentication or filesystem work.

## Decision

Register two explicit requires rules for build list and invocation list in one
owned static registry. Expose them as optional `flag_rules` in version-1 JSON
help and evaluate the same conditions at the existing validation points.
Unregistered commands omit this field. Preserve filtered command visibility,
canonical aliases and human/reference rendering.

A rule's `when` conditions are conjunctive, as are its `requires` conditions.
Conditions name registered flags and a Boolean expected value. `present` means
output flag presence, including an empty argument. `enabled` means the parsed
Boolean, preserving explicit false and last-wins behavior. `effective` means
JSON mode after global flags, environment and preferences. No supplied option
value, raw argument, path or credential enters rule state or metadata.

Output presence requires all=true. Streaming enabled without output presence
requires all=true and effective JSON=true. Keep path/limit/argument checks in
their original order, file-output precedence, disabled streaming and redundant
streaming on human-receipt file exports. These rules describe dependencies,
not all possible reasons a command may fail.

Typed local failures add cli.kind=invalid_flag_combination, the registered
rule, unmet missing conditions and validated offline help_argv to the complete
existing Problem. Use errors.As for wrapping. Preserve all envelope fields via
RawMessage, including integer precision, correlation and pagination; preserve
other CLI fields when present. API errors and unrelated local errors do not
acquire dependency metadata. Native cancellation and output handling retain
their existing classifications.

Preserve stream usage's validation_failed code and output-without-all's
invalid_request code, category invalid_request and exit 1. Human mode uses its
existing renderer. Discovery performs no API calls, correction or execution.

## Verification and consequences

A stdlib-only race core suite checks all Boolean combinations, visibility,
metadata ownership and exact Problem field/number preservation, including
short/broken writers. Public CLI tests cover discovery and aliases, no-request
rejection before login, empty/presence/false/last-wins/literal argument behavior,
unchanged human output and validation order, API correlation without rule
metadata, wrapped failures and redundant streaming on human file exports.
A built-CLI isolated journey discovers rules, receives an error without login,
follows offline help and satisfies the discovered requirement before a read.

The local core pass does not qualify full dispatch, capability lookup or the
journey. Full hosted exact-final-head checks are required. No new API/SDK
contract, parser rule or automatic repair is introduced.
