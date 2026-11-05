# ADR-20261104050000: MCP observer bindings and Task identity defaults

## Status

Accepted; extends ADR-20261104033000.

## Context

Native release readiness accepted an observer heartbeat without checking which
queue the observer monitored. CLI validation also accepted zero-valued numeric
Task defaults that Node startup rejected; Go/Python had the same mismatch for
explicit zero TTL, polling and worker concurrency. Go/Python accepted uppercase
UUIDs but used that spelling for payload authentication instead of the canonical
UUID returned by PostgreSQL, causing decryption failures with valid keys.

## Decision

Require a non-secret observer namespace matching the release's explicit local
`MCP_TASK_NAMESPACE`, and a non-secret metric destination matching
`observer_metric_app`. Check these bindings whenever native readiness is checked,
including immediately before retiring previous workers. Missing, sealed or
mismatched bindings fail closed even if an unrelated heartbeat is fresh. This
does not prove database credential identity or inspect secret values.

Preserve the CLI's accepted zero-as-default contract for the seven timing,
worker-concurrency and retry settings. Node resolves zero to the same value as an
omitted setting. Go/Python likewise default zero TTL, polling and concurrency.
Keep invalid numeric types, negative values and explicit environment overrides
subject to their existing validation.

Use the stored canonical Task UUID when Go/Python decrypt retained fields. Input
updates use that same identity for both decryption and encryption. PostgreSQL
UUID lookup remains case insensitive, while ownership and tool access checks
remain in force. Return the canonical Task ID and preserve existing envelopes;
no schema migration or payload rewrite is needed.

## Consequences

A heartbeat for another queue cannot authorize promotion or worker retirement.
Accepted default configurations start consistently across runtimes. UUID casing
cannot change payload identity or corrupt resumed input. Regression tests cover
observer drift, non-secret bindings, real startup with zeros, retained fields in
both encryption versions, input updates, cancellation and ownership. Native KVM
and real OAuth qualification remain separate acceptance gates.
