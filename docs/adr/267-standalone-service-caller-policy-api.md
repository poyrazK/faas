# ADR-267: Standalone app service-caller policy API

- **Status:** accepted
- **Date:** 2026-09-25
- **Decision:** Allow standalone apps to set the target-side `allowed_service_callers` policy at app creation and update. On PATCH, omission leaves the policy unchanged, an array replaces it, an empty array denies all internal callers, and `null` restores legacy same-account access. Project-managed and preview apps cannot mutate this source-owned policy through the app PATCH API.
- **Why:** ADR-266 made target authorization available to Compose projects, but standalone services had no way to grant or revoke callers. A nullable array preserves the existing readback shape while expressing both deny-all and policy reset without a second control field.
- **Consequences:** The API applies the same bounded, case-normalized, deduplicated app-name validation as repository scanning. The policy lives in the existing app manifest JSONB, so no schema migration is needed. Updates use the normal app-changed notification and emit old/new policy in the app audit event. This API does not create outbound bindings, introduce a short DNS alias, or alter the caller-side account default for standalone apps.
- **Rejected alternatives:** Treat `[]` as unrestricted (would contradict ADR-266); use a pointer slice for PATCH (cannot distinguish omitted from `null`); permit app PATCH on project-owned rows (reconcile would silently replace the value); add a separate policy endpoint (unnecessary surface for one manifest field).
