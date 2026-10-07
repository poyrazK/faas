# ADR-641: Operation recovery inspection and preview

## Status

Accepted — 2026-10-06. Production admission remains disabled.

## Context

Operations retain invocation uncertainty and native workflow recovery semantics.
An operator needs to inspect completed steps, uncertain attempts, retained private
files and pinned code before recording an evidenced recovery decision. Rebuilding
that view from prunable progress events would lose execution evidence. A second
resume planner would drift from the native recovery implementation.

## Decision

Add account-only recovery inspection and proposal endpoints using account read
scope and MFA. Tenant and runtime credentials grant neither endpoint. Inspection
uses durable execution summaries and the immutable workflow snapshot. It omits
inputs, outputs, raw errors, object locations and execution credentials. Prepared
file metadata does not publish a file or grant download authority. Retained means
the owned private storage receipt is still bound to the reference, without fetching
bytes; normal downloads perform their existing verification.

`safe_to_retry` previews use the existing native `workflowResumePlan` and current
code, plan, tenant, target, attempt and concurrency checks. Confirmed steps are
reused; failed steps and applicable skipped descendants are reopened. Pending
descendants continue normally. HTTP recovery starts a fresh invocation and clears
old artifact references; workflow recovery resumes its retained native run and
can reuse verified files. A succeeded proposal validates against the pinned output
schema and identifies prepared current-generation files that would be published.

Every proposal requires the observed operation generation. Platform blockers are
returned with `eligible=false`; malformed requests and stale generations use
the existing validation/conflict responses. Eligibility is an observation of
Gregale's structural checks, never evidence that external effects are safe to
repeat. Every applied recovery still requires an explicit resolution, stable
decision ID and operator evidence. Independent completion delivery stays outside
the recovery view and decision.

Inspection and proposal reads create no receipt, event, execution, file publication
or quota reservation and renew no lease. MemStore observes under its mutex without
initializing missing storage. PostgreSQL observes a repeatable-read, read-only
transaction using lock-free SQLC queries; database enforcement rejects mutations.
Apply retains the existing lock order, quota checks and native planner.

Inspection returns a SHA-256 revision of durable execution and file-binding
evidence. Optional `expected_inspection_revision` fences an apply against changes
within the same generation. Observation time, changing eligibility due to other
work, and independent delivery state are excluded. Apply checks the revision
after identical receipt replay and before mutations. Receipts made before this
optional field retain their existing fingerprints. A revision does not reserve
capacity or authorize an external effect; writer checks remain authoritative.

The CLI adds `customer-operations inspect` and `recover --preview`, including
JSON output. A blocked preview exits 4. Preview rejects evidence, decision ID and
apply-revision flags so an operator cannot confuse a read with a durable decision.
Normal apply accepts `--inspection-revision`. Go, Node and Python expose the
same wire contracts.

## Validation and rollout

Portable memory/PostgreSQL tests compare preview plans with committed native
resume receipts, retain confirmed prefixes and private files, validate typed
results, reject foreign owners and stale revisions/generations, and preserve
execution/event/receipt/quota projections across repeated proposals. HTTP tests
check account read scope, tenant rejection, app ownership, closed admission,
bounded closed bodies and no-store caching. CLI and SDK contracts cover preview
requests, blocked responses and optional apply fences.

These changes add no migration, automatic retry or admission grant. Native
execution and operational rollout qualification remain required before activation.
