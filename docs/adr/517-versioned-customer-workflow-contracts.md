# ADR-517: version customer workflow contracts and require transition evidence

## Status

Accepted for the internal HTTP implementation.

## Context

Customer Operations can declare workflow states and allowed edges, and the
application reports transitions from the same database transaction as its
business update. Gregale currently checks the edge, but the declaration has no
explicit version and the platform cannot verify that required business facts
were committed with the transition.

## Decision

Workflow declarations may set a positive integer `version` from 1 through
1,000,000. Manifests that omit it resolve to version 1. Existing immutable
definitions that predate the field retain their revision and have effective
version 1. The resolved version is included in state acknowledgements, current
state reads, and history reads.

A transition may set `operation` to scope that edge to the Operation which
produces it. It may list `requires_milestones`; requiring evidence also requires
an Operation target, and each name must be a transaction-backed milestone
declared by that Operation. The Node SDK links each distinct milestone name
reported in the application transaction to its transition report. Evidence is
bounded to 16 references per transition report.

Before the application transaction commits, Gregale validates the workflow
version and edge and checks every evidence ID and name against the submitted
milestone batch. After commit, the SDK publishes milestones before workflow
states. Gregale then verifies each evidence reference against the retained
milestone ledger for the same Operation. State report fingerprints include the
evidence references; the resolved version is server-derived so adding this
field does not break idempotent replay of reports created by older SDKs. The
combined precommit request is bounded to 131,072 bytes.

## Consequences

- An application can change workflow meaning intentionally by changing its
  version, while older declarations keep their prior immutable identity.
- A required fact and transition cannot commit separately through the Node
  transaction helper; invalid or missing evidence aborts the business
  transaction.
- Current and historical state responses explain which contract accepted a
  transition and which retained facts supported it.
- Other SDKs can submit explicit evidence references through the same API and
  receive the same validation.
