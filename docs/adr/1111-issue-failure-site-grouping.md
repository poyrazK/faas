# ADR-1111 · Opt-in failure-site issue grouping

- **Status:** accepted
- **Date:** 2026-10-11
- **Decision:** Keep ADR-380 grouping version 1 unchanged and add the explicit
  `failure-site-v1` strategy as grouping version 2. Its identity includes source
  kind, exception type, the nearest application frame to the failure, and either
  a stable `error_code` or a normalized message. An explicit fingerprint override
  takes precedence over frames, code, and message within that version/type/source.
- **Why:** Including every application caller splits one failure when it is
  reached through different handlers. Ignoring messages at a populated stack
  merges distinct failures thrown by the same function. A global algorithm change
  would disconnect existing groups from their history and resolution state.
- **Consequences:** The bounded event envelope gains `grouping_strategy`,
  `frame_order`, and `error_code`. Node, Python, Go, and OTLP expose the same
  controls. Frame order is newest-first by default for structured frames; Python
  reporters declare oldest-first. Parsed Python stacks use the last 32 frames
  of the sanitized, bounded stack text. Structured reporter frames retain the
  final failure even when stack text is truncated. Line numbers and documented build
  roots remain excluded. Message normalization masks numeric values and UUIDs;
  meaningful numeric distinctions require a stable error code or override.
  With no application frame, identity also includes route and HTTP status.
  Version 1 remains the default, including automatic HTTP failures. Opting in
  creates version 2 groups; it does not rewrite, merge, or migrate version 1
  history. Both versions retain existing limits, redaction, attribution,
  idempotency, and transactional issue lifecycle behavior. No database migration
  is required because version is already part of group identity and evidence is
  stored as JSON. Source maps, exception cause modeling, grouping previews, and
  history-preserving merge/split migrations remain separate work.
- **Rejected alternatives:** Changing version 1 in place corrupts existing
  identity semantics. Enabling version 2 globally creates duplicate history
  without an explicit transition. Hashing raw messages or line numbers creates
  excessive groups for dynamic values and routine source edits. Selecting the
  first frame unconditionally gives Python the caller rather than the failure.
