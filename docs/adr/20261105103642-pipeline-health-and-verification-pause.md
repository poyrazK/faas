# ADR-20261105103642: Pipeline health and verification pause handling

- **Status:** implemented for local/staging qualification; production disabled
- **Date:** 2026-11-05
- **Amends:** ADR-1591 and ADR-20261104134821

## Context

A recent applied checkpoint could make a pipeline in `catching_up` report
healthy. A new verification could also be accepted after pause was requested
but before the worker acknowledged it, leaving a CLI wait pending until resume.
An oversized verification key returned generic automatic-retry guidance despite
requiring an operator to reduce the key value or choose explicit recovery.

## Decision

Report current catch-up as pending even with recent applied progress. Reject new
verification requests when the desired state is not running, using the existing
revision guard. Preserve already accepted verification across pause; a CLI wait
that sees a fresh paused observation reports how to resume rather than waiting
for its deadline. Stale paused observations remain nonterminal. Explain the
primary-key size limit and its recovery separately from transient failures.

## Validation and rollout

Targeted health, control-store and CLI regressions cover these paths, including
resume and stale observations. No migration or new privilege is required.
Production admission and provider qualification remain unchanged.
