# ADR-1024: Durable recovery candidate evidence

Accepted, 2026-10-11.

Historical policy comparisons previously inferred failure status from the original policy's filter, and lost candidates when source jobs or reviews were removed.

Each newly persisted policy review now atomically appends a `candidates_observed` history event. The compact snapshot includes workflow, review/job/run associations, admitted generation, trigger, exact run status when available, preflight outcome, final preparation outcome, and the frozen policy/version. Plans, execution inputs and outputs are excluded. Read-only previews and evaluations never capture evidence. Duplicate reviews never append another snapshot.

Historical evaluation prefers the durable snapshot and falls back to retained legacy reviews. Snapshots survive review/job pruning but share the existing 30-day recording-age and 10,000-event per-app history caps. More review activity therefore consumes history capacity. Capture is prospective and limited to candidates matched by the policy; unseen failures and historical operational pause state remain unknown. Recorded preflight refusals are historical exclusions, not predictions of current eligibility.

Migration downgrade refuses while candidate snapshots remain retained.
