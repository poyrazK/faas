# ADR-341 · Ownership-aware platform-tenant reconciliation plan

- **Status:** accepted
- **Date:** 2026-09-28
- **Context:** Account owners can apply additive platform-tenant bundles and can distinguish resources created by that flow from resources that were merely linked. Operators need to compare their desired bundle with current tenant inventory before automating customer onboarding or resource cleanup.
- **Decision:** Add an account-owner `POST /v1/account/platform-tenants/{id}/reconciliation-plan` endpoint for an existing tenant. Validate desired additions using the existing apply planner in dry-run mode, then report a deterministic inventory diff. Omitted resources with managed provenance are removal candidates; omitted resources without provenance are retained as unmanaged. A `surface_ids` link does not declare the surface's desired hostname set; only entries in `surfaces` plan hostname changes.
- **Safety:** Planning is read-only. It does not detach surfaces, unlink or revoke consumers, remove hostnames, or delete resources. Managed provenance is a necessary boundary for a future apply operation, not sufficient authorization to mutate. Conflicts and invalid bundles fail closed. Any future operation that applies removals must be explicit and must revalidate current ownership and state.
- **Consequences:** Platform operators can inspect the exact additive and ownership-aware removal candidates before adoption of automated reconciliation. Existing apply semantics stay additive and retry-safe while the plan/apply workflow is delivered in separate steps.
- **Rejected alternatives:** Inferring managed ownership from a tenant link; mutating omitted resources during the existing apply call; treating a surface-ID link as an instruction to prune its hostnames.
