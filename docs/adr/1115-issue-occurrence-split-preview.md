# ADR-1115 · Issue occurrence split preview

- **Status:** accepted
- **Date:** 2026-10-11
- **Decision:** Provide a read-only preview within one original fingerprint identity, including linked members. Match every populated selector by exact equality against sanitized stored exception type, error code, route, and source kind. Require at least one selector; no regular expressions or raw payload search.
- **History:** Count all retained occurrences, independently of dashboard pagination and impact windows. Report matching, nonmatching, and unavailable lifetime history separately. Unavailable evidence cannot be assigned to either side. Return at most ten matching occurrence IDs, newest first.
- **Consistency:** Read identity, current canonical membership, counters, and evidence in one repeatable-read transaction. App scoping applies before examining evidence. Preview changes no counters, membership, lifecycle, activity, or future routing.
- **Next:** Durable rules and occurrence reassignment require a separate decision covering retry identity, precedence, retention, and release accounting. This preview does not authorize or perform a split.
