# ADR-20261114090000: Company project manifests may declare async routes

Status: Accepted (2026-11-14)

## Context

A project manifest's `async_routes` reconcile async edge rules on the
project's apps during deploy. Company-project deploys refused any manifest
with `async_routes` (409, "async-route bindings require separate organization
ownership support"), added while edge rules were scoped by creator account
(Hunt 24). That scoping is now mitigated: the serving reads carry an
organization clause (ADR-091 amendment 2026-10-31). Async destinations are
webhooks owned by the same app (`ValidateEdgeRuleAsyncDestinations`), and
company members already write the same rules through
`POST /v1/apps/{slug}/edge-rules` (projects review 2026-11-12, follow-up 2).

The refusal was a role question, not an ownership one: a deploy needs
`deploy_project` (developers), but writing edge rules directly needs
`manage_app_security` (owners and admins).

## Decision

A company-project deploy or scan whose manifest declares `async_routes`,
including an empty list, which clears manifest-owned async routes, needs
`manage_app_security` as well as the deploy role
(`projectaccess.AuthorizeManifestAsyncRoutes`). Members without it get 403
before anything is created. The rules are written as for a personal project,
with the project's resource owner as their account, and `--no-triggers` still
skips them.

## Consequences

- Developers deploy company manifests without `async_routes`, or ask an owner
  or admin to deploy the change.
- Edge-rule scoping, plan limits and destination checks are unchanged.
