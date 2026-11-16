# ADR-20261115100510: Structured corrections for unknown CLI commands

Date: 2026-11-15

Status: accepted

## Context

Human command errors already suggest likely spellings. JSON errors retain that
advice only inside prose, so agents must parse a sentence to discover a valid
command or help topic. ADR-20261115082128 supplies a CLI-local `cli` extension
for flag errors; this extends the same contract without adding commands.

## Decision

Root unknown-command errors, the shared nested-command renderer, the invocation
dispatcher and unknown JSON help topics attach structured advice. The `kind` is
`unknown_command` or `unknown_help_topic`. `suggested_commands`, when present,
is an array of argument arrays containing only registered canonical command
words. Unknown input, remaining arguments, flag values, targets, connection
profiles and credentials are never copied into those arrays.

The existing edit-distance threshold and tie suppression apply. Candidates
come from the offline, capability-filtered customer manifest. Compatibility
paths are resolved to canonical paths; hidden commands are not candidates.
The CLI validates every proposed path through the same help lookup. A
`help_argv` array identifies the corrected command, deepest visible parent, or
root JSON help. For a help-topic error, the suggested command is itself JSON
help. The CLI never executes a correction or replays the failed invocation.

Unknown command errors carry a typed local error to the existing Problem
writer, including through `%w` wrapping. Nested dispatchers keep their existing
Problem code and prose. API errors follow their existing early branch and omit
this extension. JSON root suggestions use offline capability state rather than
refreshing it on an invalid invocation. Human behavior, wording, exit codes and
stdout/stderr routing remain unchanged. Other legacy dispatcher errors retain
their existing behavior until they adopt the shared renderer explicitly.

## Validation

Public-dispatcher tests cover root and nested typos, canonical/compatibility
paths, help-topic corrections, ambiguity, hidden commands, raw-input exclusion,
following help arrays, zero API requests and exact human diagnostics. API
regression coverage retains server codes and request IDs without `cli`. An
isolated built-CLI journey follows help without invoking the suggested work.
Local probes and hosted qualification are reported separately in the PR.
