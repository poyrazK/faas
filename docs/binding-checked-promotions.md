# Binding-checked environment promotion

Use `gregale projects environments promote shop --from staging --to production --sync-config --require-bindings --yes --wait --progress` to promote an approved, qualified source release without activating targets before their binding checks pass. The preview must support release graphs and revision retention. Secrets remain target scoped.

The CLI sends one admission POST to `/v1/projects/{slug}/environments/{environment}/promote-with-bindings`. A running operation returns HTTP 202 with `bindings_required: true` and its promotion ID. Older servers reject this route. Reusing the same idempotency key returns the same operation; switching its binding-check mode is rejected.

The backend reserves and stages dark candidates, then checks every exact deployment against its immutable prepared configuration. A blocked operation leaves the active graph and desired configuration intact. Its status contains `bindings_check`, including exact membership, graph digest, member reports and blockers. The worker does not run probes or application handlers. Run the verification commands printed by `--progress` or `status`; service probes include both the caller candidate and the selected service target.

A backend restart resumes the saved candidates. Once fresh checks pass, the backend commits the graph, captured settings, environment configuration, feature flags, audit entry and successful operation receipt together. Concurrent graph or configuration changes cannot be overwritten with stale evidence.

Resume a local wait with `gregale projects environments status shop PROMOTION_ID --to production --wait --progress`. Waiting uses GETs only. A local timeout exits 3 and leaves the operation running; JSON output includes the saved operation, verification commands and resume command.

Enabled environment queue definitions remain blocked when exact deployment dispatch proof is unavailable. Prepared consumer records and app-wide consumer health cannot qualify them. An explicit empty or disabled pinned queue collection never inherits global bindings. Binding-aware rollback is a separate capability; restore through another checked promotion. Native Linux/KVM acceptance remains an operator qualification step.
