# ADR-1357: Server-generated binding verification reviews

- **Status:** Accepted
- **Date:** 2026-10-14
- **Context:** CLI and SDK callers must otherwise reconstruct verification plans and hash inputs before creating durable receipts. Duplicate planning risks mismatched selections and consent.
- **Decision:** Add read-authorized POST review endpoints for apps and prepared project release graphs. Return exact deployments and service recipients, public inventory and policy, plans, blockers, required effect consent and opaque server-generated digests. Missing app deployment selects the current live deployment once. Reviews neither capture private task commands nor create receipts, consume admission capacity, publish messages or run probes.
- **Meaning:** Ready means a complete executable plan is available, not that verification passed. Blocked reviews have no digest. Probe feature availability is included in readiness. Required consent is informational; review never performs the requested effects.
- **Graph authority:** Re-derive the prepared forward, rollback or deactivation manifest before and after member reads. Bind the aggregate digest to that manifest, all member digests and effect consent. New callers forward the aggregate digest at creation; omission remains compatible with existing clients. Creation rechecks current authority and member plans before persisting receipts.
- **Clients:** CLI dry runs and durable creation use server reviews. Go, Node and Python expose typed review methods. Callers forward returned selections and digests without implementing hashing. Existing receipt validation and execution gates remain in force.
- **Validation:** Read-only domain and HTTP review tests; review-to-create/resume contracts; stale policy, changed manifest and consent rejection; exact target and CLI recovery coverage; SDK generation, contracts and lint.
