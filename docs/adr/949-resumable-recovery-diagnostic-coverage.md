# ADR-949: Resumable recovery diagnostic coverage

Date: 2026-10-11
Status: Accepted; production qualification pending
Related: ADR-939, ADR-942, ADR-947, ADR-948

## Context

Exact diagnostic reads are capped at 50. Restarting each pass at the first
recipient leaves larger selections without coverage and repeated failures can
starve refreshes. Operators need separate progress without changing delivery
status, recovery attribution or watch settlement.

## Decision

Opt-in diagnostics rotate through the fixed recovery selection. Read queryable
unread recipients first, then failed diagnostic reads, then refresh already
sampled recipients. Each category has its own cyclic index. The same 50-read
cap applies; when failed and sampled recipients compete after unread work,
reserve one read for a successful-sample refresh if at least two slots remain.
Independent cursors prevent persistent failures from repeatedly selecting the
same prefix and prevent successful samples from starving.

Do not diagnose unobserved/failed receipt rows; defer them until receipt evidence
is observed. Unsupported recipients identified by current status or reviewed
reason are excluded. Output remains in original selection order. Advance a
category cursor only after attempting its request, including failed requests;
cancellation before a request leaves it pending. No diagnostic retry loop is
introduced and diagnostic reads do not consume receipt retry budgets.

Add optional diagnostics_progress to results: next_index, retry_next_index,
refresh_next_index, target_count, remaining, deferred, retained, reads, counts
and complete. Reads counts only this enrichment invocation. Remaining includes
relevant rows without a validated diagnostic sample, including deferred receipt
rows and failed/invalid diagnostic reads. Complete means all relevant rows have
samples, including authoritative unavailable or acceptance_replaced responses;
it never means blockers are absent or delivery/recovery succeeded. Deferred
also counts retained samples whose receipt row currently cannot be queried.

Resume binds the full ordered selection snapshots and exact apply attempts
before carrying diagnostics. Validate precise available-recipient identities,
known routing states, coverage, observation timestamps and unavailable reasons;
discard invalid samples for a fresh read. Preserve read-error scheduling state.
Saved aggregate counters are ignored and rebuilt; out-of-range cursors reset
rather than skipping recipients. Retained samples keep their original observed_at
and gain retained=true until reread. They are historical observations, not fresh
server claims or an atomic snapshot. URLs in samples are never followed.

This refines ADR-947/948's unconditional discard policy only when diagnostics
are explicitly enabled. Ordinary status reads still discard saved diagnostics.
Explicit refresh starts a new diagnostic coverage cycle; resume and automatic
watch receipt refreshes retain diagnostic samples and scheduling progress.
Callbacks and existing atomic checkpoints include progress. Watch receipt read failure
preserves prior diagnostic progress without extra diagnostic reads; interruption
preserves partial diagnostic samples where the SDK attaches partial results.

Go adds ResumeCommitRecoveryDiagnostics alongside the existing enrichment helper
and public progress DTO. Node diagnoseCommitRecoveryStatus accepts an optional
previous status after the signal; Python adiagnose_commit_recovery_status accepts
previous as a keyword. Status/watch helpers and CLI resume wire this automatically
when diagnostics are enabled. Input snapshots remain detached from caller data.

Delivery counts/completeness, freshness, change comparison, acknowledgement
attribution, execution settlement, monitoring stop reasons and CLI exit semantics
are unchanged. A watch can settle with incomplete diagnostics; operators can
continue diagnostic coverage through ordinary status resume. CLI progress reports
diagnostic counts separately without treating cursor-only movement as a delivery
change. No server route, SQL or routing behavior changes are needed.

## Consequences

Large selections gain bounded coverage across invocations and checkpoints.
Oldest evidence is not automatically expired: retained flags and timestamps make
its age visible while cyclic refresh gradually replaces it. Persistent failures
remain explicit; they do not prevent other recipients from being inspected.
Production large-selection, interruption and restart qualification remains pending.
No local tests are added or run; compliance registries are unchanged.
