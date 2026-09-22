# ADR-219 · Preview-scoped internal service resolution

- **Status:** Accepted
- **Date:** 2026-09-22
- **Decision:** A project PR preview resolves an internal service name to the
  workload with the same `(account_id, project_id, preview_pr_number)` before
  considering the production app slug.
- **Why:** Preview traffic should use an isolated dependency when one exists,
  while retaining an explicit, policy-controlled fallback during incremental
  rollout of project-wide preview provisioning.
- **Consequences:** Preview workload identity is unique per project and PR;
  the gateway distinguishes preview-to-preview from preview-to-production
  traffic; provisioning the sibling workloads remains a separate milestone.

## Context

Internal service names historically resolved directly through `apps.slug`.
PR previews are apps too, but their generated slug contains the PR number and
parent slug. A preview of `web` calling `billing.svc.gregale` therefore found
production `billing`, even if an isolated `billing` preview for that PR was
already present.

ADR-095 intentionally shipped one-app previews. The project and workload
columns added later provide enough durable identity to make routing
environment-aware without coupling the gateway to build or provisioning
lifecycle. The preview production-dependency policy supplies a safe fallback
when that environment is incomplete.

## Decision

For a caller with `preview_of_slug != null`, `preview_pr_number > 0`, and a
project identity, the service resolver performs this ordered lookup:

1. Find a live preview app matching the caller's `account_id`, `project_id`,
   `preview_pr_number`, and the requested `workload_name`.
2. If found, route to that preview app and use its protocol and WebSocket
   posture.
3. If absent, resolve the requested name as a production app slug. The
   existing authorizer then applies `preview_service_policy`: `deny` rejects
   before discovery/wake and `allow_marked` permits the marked call.

The complete tenant/project/PR tuple is mandatory. A row in another account,
project, or PR is never a candidate. Production callers, standalone previews,
developer sessions (`preview_pr_number=0`), and legacy previews without a
project identity retain global slug resolution.

The production workload uniqueness index excludes preview rows. PR previews
instead use the unique key `(project_id, preview_pr_number, workload_name)`.
This lets production and preview copies retain the same workload identity
without permitting two copies inside one PR environment.

## Observability

- `gateway_service_preview_to_preview_total` counts calls resolved inside the
  caller's PR scope.
- `gateway_service_preview_to_production_total` counts permitted production
  fallbacks.
- `gateway_service_call_total{outcome="preview_denied"}` counts rejected
  production fallbacks.

These counters are fleet-wide and unlabelled, preserving bounded cardinality.

## Non-goals

- Provisioning or building every project workload for each PR.
- Speculative wake-ahead along `depends_on` edges.
- Changing preview quotas, TTLs, or teardown.
- Applying PR scope to ad-hoc developer sessions or named project deployment
  environments.

## Rejected alternatives

- **Clone the whole project in this change.** That combines routing with build
  fan-out, quota accounting, idempotent app creation, GitHub status, and
  teardown. The resolver should be independently safe before provisioning
  relies on it.
- **Derive the target preview slug.** Slugs are transport-facing names, not
  the project workload key, and truncation or future slug normalization would
  make derivation brittle.
- **Fall through to a preview from another PR.** This breaks isolation and can
  route a request into code from an unrelated commit.
- **Return not-found whenever the sibling is absent.** A policy-controlled
  production fallback is required while preview provisioning remains
  incremental and preserves the explicit `allow_marked` compatibility mode.
