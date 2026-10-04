# ADR-509 · Per-target binding adoption diagnostics

- **Status:** accepted
- **Date:** 2026-10-03
- **Decision:** Include derived `reload_status`/`reload_reason` and
  `application_ack_status`/`application_ack_reason` on each authorized
  workload-secret adoption target. Status values are `current`, `failed`,
  `stale` or `unknown`. Reason values are closed, stable codes derived only
  from observed metadata. They never include generation IDs, raw errors,
  credentials or fingerprints. `gregale bindings check` prints the non-current
  targets with deployment, instance, workload and secret key context; JSON and
  SDK clients receive the same per-target fields.
- **Why:** Aggregate counts and a binding-level blocker do not identify which
  resident workload prevents strict promotion or whether it needs a current
  projection, a reload retry, a generation-aware helper or a current ACK.
- **Consequences:** The existing single-snapshot adoption data is sufficient;
  no migration or additional host read is needed. Aggregate counts and strict
  promotion semantics continue to derive from the same per-target classifiers.
  Older servers may omit these additive response fields; clients must continue
  to handle that response shape.
- **Validation:** Keep status/reason derivation aligned with the existing
  aggregate evaluator and make the stable codes part of the OpenAPI and SDK
  response models. The CLI omits generation values and raw diagnostic text.
- **Rejected alternatives:** Adding another endpoint duplicates the adoption
  read and risks inconsistent snapshots. Returning raw host errors or generation
  values adds sensitive, unstable diagnostic data without improving recovery.
