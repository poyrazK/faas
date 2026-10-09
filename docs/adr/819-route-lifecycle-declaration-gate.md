# ADR-819: Lifecycle declaration reviews and canary gate

Status: Accepted

## Context

Published sunset guidance is a commitment clients may use to plan migration.
Rollouts can erase metadata, accelerate sunsets or change successors without
breaking the structural OpenAPI contract.

## Decision

Add a pure captured-document lifecycle comparison and an advisory Gregale
`routes lifecycle declarations` report with hashes and deployment provenance.
Flag removed deprecation/lifecycle fields, earlier sunsets, successor changes,
removals before sunset, and deprecated removals without an announced sunset.
Unsupported/missing captures and invalid metadata remain incomplete findings.

Reuse the existing canary route gate's report/enforce modes on canary advances,
including worker advances. Compare candidate captures to every positive serving
sibling in the same scope and the retained route-removal baseline. Lock captures
inside the advance transaction and use its database clock. Recompute lifecycle
findings independently from saved automatic route-check freshness. Report only
metadata reason codes in the existing decision/audit shape. Abort stays available.

Successor URLs are not explicit operation mappings. Changes require fresh
compatibility review; this increment introduces no durable review waiver, so
enforced advances reject these review-required changes. Advisory reports neither
approve route removal nor bypass structural contract checks.

## Consequences

No API shape or database schema change is required. Existing entitled owners
configure the gate with optimistic revisions. Enforced advances now require
usable serving baseline captures as well as existing candidate evidence.
Declaration enforcement remains scoped to canary advances; initial activation,
ordinary promotions and direct traffic use existing gates. Wider declaration
coverage and durable successor-review receipts are subsequent increments.
