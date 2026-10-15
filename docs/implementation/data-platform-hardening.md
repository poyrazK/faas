# Data platform hardening

Assessment date: 2026-10-15. Starting source: `8b66d31d501b3fffb8428727f176e048e4e539d7`.
Capability promotion continues to follow `pkg/productcap/catalog.json` and
`docs/capabilities.md`. This plan enables no production gates.

## Work sequence

| Priority | Work | Completion evidence | State |
| --- | --- | --- | --- |
| P0 | Qualify one supported managed PostgreSQL/Data API/Auth journey | Current provider v8 report, matching native guest report and hosted customer canary, including rotation, migrations, park/restore and cleanup | Pending isolated provider, account and native-host setup |
| P1 | Bound existing realtime channel grants | Idle and buffered-delivery revocation, callback outage recovery, endpoint changes, focused tests and race checks | First implementation: ADR-1331; deployed qualification pending |
| P1 | Complete restore-to-serving activation | Writer/session drain evidence, atomic binding publication, snapshot invalidation, application checks and explicit recovery/backout procedure | Pending; keep customer activation unavailable |
| P1 | Establish production data capacity and health contracts | Aggregate connection budgets, pool/query timing, controlled SQL probes and concurrent app/database wake load tests | Pending |
| P1 | Release supported client packages | Owner-approved SDK licensing, immutable versioned releases, provenance and tested CLI/runtime compatibility | Pending licensing and registry decisions |
| P1 | Reconcile complete provider spend | Provider allowances and branch charges, delayed corrections, deletion settlement and qualified budget cutoffs | Pending provider/exporter qualification |
| P2 | Reconcile capability discovery, docs and onboarding | Dedicated maturity entries, consistent customer guides and measured first verified transaction | Pending |
| P2 | Expand provider and change-feed coverage | Qualified operation matrix, orphan reconciliation, reviewed feed payload/latency limits and customer-driven backfill/partition support | Pending |

## First slice: retained channel authorization

ADR-1331 refreshes the existing application callback for retained WebSocket
channels using SSE's 30-second positive grant lifetime. Idle polling and outbound
delivery enforce expiry; callback denials remove subscriptions while outages
close the connection for retry. Existing ticket and durable access controls keep
their own checks.

Required evidence includes concurrent refresh, policy/token replacement,
callback timeout/denial classification, idle route/presence removal, and queued
message/presence/signal/read-receipt/checkpoint rejection after expiry. Run the
full realtime package, race checks and applicable repository policy checks.

Local verification passed for the full realtime package with and without `-race`,
changed-line lint, documentation links, four live-docs harness tests, ADR number
uniqueness, runbook SQL, text encoding, shell quoting and the gosec rule baseline.
Earlier race attempts intermittently timed out in the existing
`TestManagerKeepsIdleConnectionAcrossPongs`; its original-source baseline passed
ten runs and the final full race run passed. No data race was reported.

Follow-up work must address individual Auth session binding and WebSocket token
reverification. The current Auth callback authorizes a subject with at least one
active session. This slice does not claim session-specific revocation, direct
message/inbox coverage or deployed fleet acceptance.

## Release boundaries

Local fixtures and portable CI cannot substitute for live provider and native
acceptance. Reuse the existing managed PostgreSQL qualification tooling and Data
API staging harness. Record exact source/configuration identities, expiry and
cleanup evidence before promoting a capability. Do not infer readiness from a
healthy metadata read or an installed feature flag.

Public package publication, production provisioning and restoration cutover are
separate changes. Complete their reviewable implementation and required evidence
before changing their existing release gates.
