# ADR-1112 · Verified issue grouping explanations

- **Status:** accepted
- **Date:** 2026-10-11
- **Decision:** Compute an optional, read-only grouping explanation for each
  retained occurrence in issue detail. Share the grouping decision with the
  fingerprint implementation and verify its hash against the saved issue
  fingerprint using the saved grouping version before returning evidence.
- **Why:** A fingerprint alone cannot explain false splits or merges. Operators
  need to see which sanitized application frames, override, error code, or
  normalized message made an occurrence belong to a group.
- **Consequences:** The authenticated detail API and dashboard expose only the
  selected sanitized evidence, including canonical file paths and zero-based
  indices into occurrence frames. Fallback explanations identify route/status
  evidence when usable application frames are missing. Unsupported versions or
  mismatched evidence omit the explanation and display an unavailable notice.
  Historical evidence is not revalidated against current retention or rewritten.
  Existing identity, lifecycle, pagination, authorization, and retention are
  unchanged. No database migration or new ingest fields are required.
- **Rejected alternatives:** Duplicating the selection algorithm in the UI can
  drift from actual grouping. Displaying an unverified reconstruction can
  misrepresent historical evidence. Replaying normalization at read time can
  reject older retained events or apply a changed redaction policy.
