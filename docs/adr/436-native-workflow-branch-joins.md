# ADR-436: Native workflow branch joins

Status: Accepted

## Context

ADR-435 guards select conditional paths, but an ordinary step depending on both
paths inherits an inactive branch's skip. Customers must duplicate every shared
continuation. Automations need a durable way to merge those paths and explicitly
select an output without customer code or a race between branch completions.

## Decision

Add `join: {output_from: [branch_b, branch_a]}` as an exclusive native step target.
`depends_on` names all terminal branches; `output_from` is a permutation of those
dependencies in explicit selection priority order. Both lists require 2-128
branches, with the bound centralized in `pkg/api/limits.go`.

A join waits until every direct dependency is terminal. Succeeded dependencies
are eligible. Skipped dependencies are eligible only when caused by a persisted
false guard, or by propagation from such a branch. Validate the entire skip
ancestry, including sibling dependencies: a generic `dependency_skipped` reason
alone does not prove inactivity. Unfinished ancestors keep the join pending;
failed/dead ancestors, cancellation, missing/legacy causes and inactive exception
routes block it with `dependency_failed`. Memoize shared ancestry traversal.

Select the first succeeded dependency in `output_from` order, independent of
manifest/storage/completion order. Persist `{source: step_name, value: output}`
as the join's output. Empty successful outputs become JSON null; JSON numbers,
arrays, objects and scalar types retain their existing precision and meaning.
Downstream steps use ordinary templates, such as
`{{steps.merge.output.value.customer_id}}`. A join with all branches conditionally
inactive is skipped with `dependency_skipped`, propagating inactivity normally.

The store locks the run before the join step, matching cancellation, output
completion and recovery. Eligibility, output selection and the terminal
transition commit together. Repeated resolution returns the first result;
explicit and lease-expiry recovery leave terminal joins intact. A join never
starts an executor or allocates an attempt, and the ordinary start operation
refuses join targets. Existing inspection exposes its status, finish time and
selected source/value. Downstream action retries retain their normal input
snapshot and attempt history.

Join steps cannot have input, method, guard, timeout, retry or exception routes.
They cannot be exception-handler targets or depend directly on exception
handlers. Failure compensation remains governed by the existing exception
rules; a join does not convert a failed workflow into success. This first
increment supports conditional branch convergence, not first-completion joins,
optional references, loops or executable mapping expressions.

apid validates customer intent; schedd owns resolution through the state store.
JSON/YAML manifests, automation drafts/publication, OpenAPI and generated
Node/Python SDKs accept the target. No frontend or VM lifecycle changes apply.

## Rollout and rollback

The result reuses existing output, status and skip-reason columns, so no new
migration is required. The preceding guard migration remains required. Deploy
updated apid and schedd together before publishing definitions containing joins;
the existing workflow runtime gate applies.

Before downgrading, pause starts and drain/cancel joined runs, then replace
joined API and manifest definitions with definitions older binaries can decode.
No production flags or configuration are changed by this implementation.

## Verification

Strict JSON/YAML and DAG validation reject ambiguous targets, priority lists,
unsupported options and exception routing. Memory/PostgreSQL race tests cover
all-terminal readiness, transitive conditional skips, failed sibling ancestry,
unknown causes, deterministic output selection, cancellation and recovery.
Scheduler tests exercise shared continuations, all-inactive branches, failures,
declaration order and continuation retries. API and SDK tests preserve the join
definition and inspect its committed output without allocating attempts.
