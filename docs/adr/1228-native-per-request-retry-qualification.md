# ADR-1228: Native per-request retry qualification

Date: 2026-10-13
Status: Accepted

## Context

A portable strict assertion can pass while an installed gateway lacks correlation
support. An aggregate retry average also hides individual retry storms.

## Decision

Extend the native scenario acceptance catalog to thirty cases and verdict version
18. Four cases use the instrumented retry caller: a bounded positive control,
an eleven-attempt outlier with a passing average, missing outgoing propagation,
and missing final completion receipts. Negative cases require the matching strict
failure evidence; unrelated command failures cannot qualify them.

Fixed startup modes retain namespace opt-in, finite attempts and deadlines, joined
work, existing disposable provider and platform-owned cleanup. Shared state
conformance checks persisted hash counters and rejects late old-generation batches
after replacement. No state table, API route or metric label is added.

## Consequences

Earlier verdicts do not qualify strict retry limits. Native results require the
designated native x86_64 KVM host and installed platform provenance. Portable
harness and fixture tests qualify their contracts only.
