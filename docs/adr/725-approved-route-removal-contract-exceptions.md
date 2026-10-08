# ADR-725: Approved route removal exceptions in the contract gate

## Status

Accepted

## Context

The contract gate treats a removed operation as a breaking change. Route-removal
approvals require a captured candidate at zero traffic, so rejecting that
candidate during activation prevents the approval workflow from starting.
Selecting the newest snapshot also lets a dark candidate displace the serving
contract as the comparison baseline.

## Decision

Production activation and API traffic increases compare against the serving
contract. A configured removal policy retains its baseline through partial
canaries. Explicit zero-traffic candidates may omit whole operations while all
other confirmed breaks and incomplete comparisons still block activation.

Increasing traffic may waive only whole-operation removal findings covered by
an unexpired server approval under an enforced removal policy. Both captured
contracts must project to the exact canonical snapshots being compared.
Response-field removals, required-field changes, type changes, and unknown
comparisons retain their blocking behavior. Report mode and local attestations
do not grant contract exceptions. Historical rollback gates remain strict.

An internal transaction fence pins the approval ID, policy revision, baseline,
capture digests, and canonical snapshot digests. PostgreSQL checks it during
traffic activation even if policy mode changes to report after preflight.
Capture and snapshot rows are held for the write; document locks precede policy
locks to match capture observation updates. Activation also validates the newly
projected snapshot before committing. Public request fields cannot set fences.

## Consequences

- Operators can capture a dark removal candidate and complete server approval.
- A valid removal approval cannot suppress unrelated breaks or unknowns.
- Evidence changes after preflight require a fresh check and roll back traffic.
- Captures that differ from the compared contract snapshots fail closed.
- The existing contract feature flag and production scope targeting remain.
