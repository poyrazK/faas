# ADR-20261115082128: Structured corrections for CLI flag errors

Date: 2026-11-15
Status: Accepted

## Context

The human flag-error writer offers typo suggestions, expected value formats
and global placement guidance. JSON mode returns only a normalized parser
message. An agent must parse prose to find a correction despite the offline
command contracts introduced by ADR-20261115071638. UX specification §3.2
requires a useful structured CLI surface.

## Decision

Use one diagnostic builder for human and JSON flag-parser errors. Keep human
wording, parser behavior, API Problem codes, exit codes and streams unchanged.
Add an optional CLI-local `cli` object to parser Problems. Its `kind` is
`unknown_flag`, `missing_value`, `invalid_value`, `misplaced_global_flag`, or
`invalid_flag` for an unclassified parser error. Registered flags can supply
`flag`, `suggested_flags`, `expected_type`, `value_marker`, `allowed_values`
and `placement`. Expected types are `boolean`, `integer`, `number`, `duration`,
`enum`, or `value` for a documented format without a narrower parser type.

Suggestions use the actual FlagSet and retain existing ambiguity thresholds.
Types come from parser types; value markers and closed choices come from the
manifest. Global placement comes from the shared discovery registry. Never
copy supplied values, raw argv or unknown flag names into this metadata.

`help_argv` is an argument array pointing to offline JSON help for a validated,
visible command, with root help as the fallback. It is advice: no arguments
are rewritten, retried or executed. Build this array from the same visible
manifest and canonical spelling as JSON help, without refreshing capabilities
or making API calls.

## Consequences and validation

The extension is local to CLI serialization; server Problems and SDK wire
contracts do not change. Other local validation and API errors omit `cli`.
Existing fields remain compatible. A value format does not promise a value
meets command-specific limits or server policy.

Public dispatcher tests cover typo suggestions, types, missing values,
closed choices, global/local profile scope, credential isolation, exact human
output, one Problem on stderr, unchanged exits and zero requests before
validation. An isolated built-CLI journey follows returned help arguments to
discover a suggested flag without submitting a deployment.
