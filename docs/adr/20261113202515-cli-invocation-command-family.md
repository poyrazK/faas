# ADR-20261113202515: One invocation command family

Status: Accepted
Date: 2026-11-13
Related: ADR-20261026145832 (command tree), ADR-20261113183052 (async wait)

## Decision

Use one public command family for submitting and inspecting calls to deployed
apps and functions:

- `gregale invocation run APP [flags]`
- `gregale invocation list [flags]`
- `gregale invocation get ID [flags]`
- `gregale invocation wait ID [flags]`

The existing `invocation watch` remains in the same family. The `run` leaf maps
to the existing `invoke` implementation through the command-tree alias table.
There is one flag parser and submission path. Invocation results, async receipts,
authentication, request routing and exit codes retain their existing contracts.
Async submission with waiting uses `run APP --async --wait`; this decision does
not change synchronous versus asynchronous defaults.

Amend the command tree's daily shortcut list: `invoke` becomes a compatibility
name rather than a second public entry point. Existing `invoke APP` calls still
run unchanged, including apps named `run`, `list`, `get` or `wait`. The historical
`invocations list/get/wait` spellings remain callable. They are not additional
names in root help, completion or the published command manifest.

Help, completion, generated reference, customer docs, starter examples and agent
instructions teach the canonical family. Reuse the repository's command-tree
rewriters for printed CLI guidance and customer documentation. Historical ADRs
and specifications retain the commands they recorded.

This consolidates only deployed-app invocation submission and its ledger. It
does not change sandbox Runs, Jobs, workflows, event delivery or HTTP telemetry,
which have different resource lifecycles and API contracts.

## Verification

Tests exercise canonical submission, both sync and async routes, compatibility
submission and verb-shaped app names. Discovery checks require run/list/get/wait
in the invocation manifest and reject duplicate roots in help/completion.
Built-CLI journeys submit and inspect through the same family, and the existing
wait, terminal-state, timeout, interruption and alias-routing regressions remain
part of validation. Command-tree checks keep generated guidance consistent.
