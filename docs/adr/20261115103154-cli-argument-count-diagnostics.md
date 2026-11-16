# ADR-20261115103154: Structured CLI argument-count diagnostics

Date: 2026-11-15

Status: accepted

## Context

Invalid positional counts currently produce usage text. After structured flag,
JSON-input and command corrections, agents still infer a missing invocation ID
or an unwanted positional from prose. The parser already knows those rules.

## Decision

Add an explicit argument-count guard after flag parsing and before authentication
or work. Its caller supplies minimum/maximum counts, registered argument labels,
command name and the existing usage message. Initial adoption covers invocation
get/wait, the shared noArgs helper and rejectUnexpectedFlagArgs. This does not
infer arity from help text, change flag ordering or add target/ID validation.

On a count failure, the existing CLI-local cli extension gains kind
missing_argument or unexpected_argument and an arguments object with labels,
expected_min, expected_max and received_count. All counts, including zero, are
serialized; labels is an array even when empty. Supplied values, argv, flags,
profiles and credentials are absent from this metadata. Existing usage detail
and human rendering remain unchanged.

help_argv uses the existing offline visible/canonical help lookup and falls
back safely to root. Nothing reads additional input or executes a correction.
A valid count proceeds to existing option, authentication and API checks.
Those failures do not acquire argument metadata. Existing Problem codes,
categories, exits, streams and request IDs remain unchanged. No API/SDK wire
contract is extended.

## Validation

Public dispatcher tests cover missing/extra invocation IDs, flag values,
literal separators, compatibility spelling, zero-argument and flag-only leaves,
following help, zero requests on refusal, unchanged human usage, other parser
errors and valid counts reaching the same API error/correlation path. A built
CLI journey discovers the requirement without submitting work. Local helper
probes and full hosted qualification are reported separately in the PR.
