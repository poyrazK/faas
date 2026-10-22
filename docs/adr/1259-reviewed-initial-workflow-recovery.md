# ADR-1259: Reviewed first recovery from captured workflow failures

Accepted, 2026-10-12.

ADR-1025 captures initial failures, while ADR-1026's retry observer requires a saved recovery parent. Provide an independent, read-only initial failure inbox and live first-recovery preview. The inbox includes only retained generation-zero failures, is policy independent, newest first, paginated and explicitly partial. Cursors bind account/app/workflow and freeze the observation ceiling. Capture remains prospective with the existing 30-day/10,000-entry per-app limits; pruning may remove evidence between pages.

Preview reads the captured failure and current resume diagnostics in one memory lock or PostgreSQL repeatable-read snapshot. It reports generation/status changes, missing runs, safety blockers, operation ownership and capacity using the existing diagnostics. It never trusts the cached retry eligibility observation, creates no state, and returns a selectable target only when currently eligible.

The CLI can save that target as an existing version-one run recovery plan. An optional `failure_evidence_id` accompanies its generation-zero target. Explicit `recovery-start` submits the plan through the existing native durable job API, preserving idempotency, quotas, operator reasons and terminal evidence. The direct per-run journal executor rejects these evidence-backed plans. Creation and admission check that evidence remains retained and matches app, workflow, run, generation and failure status. Native resume still atomically enforces generation, safety, ownership and capacity. Changed or expired evidence is rejected at creation or skipped at admission; repeating a committed request returns its retained job.

This is operator-reviewed first recovery, not a new policy decision type. It does not broaden `automatic_retry`, simulate historical preparation, change retained eligibility observations, or introduce a migration or quota. The job's frozen selection carries the failure identity for inspection. Existing generation-fenced manual recovery selections remain supported.
