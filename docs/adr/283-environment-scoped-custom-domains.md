# ADR-283: Environment-scoped custom domains

- **Status:** accepted
- **Date:** 2026-09-27
- **Decision:** A custom domain may optionally belong to one project environment. Existing domains remain application-wide. Environment-scoped domains route only to the active deployment graph for that exact environment; they never fall back to the app-wide production route. Environment domain targets are resolved dynamically and are not placed in the gateway's route cache.

## Context

Project environments can own configuration, workload variables and secrets,
routes, policies, and managed resource bindings. Custom domains are still
attached only to an application, so a staging hostname cannot safely point at
the staging release without sharing the app's ordinary route. This makes
environment isolation incomplete and can send staging traffic to production.

## Decision details

- `custom_domains.environment_id` is nullable and references a project
  environment. `NULL` preserves current app-wide behavior; deleting an
  environment removes its scoped domain claims instead of widening them to
  production.
- A scoped domain's application must belong to the environment's project.
  The API validates this relationship and the gateway rechecks it before
  routing.
- Scoped domain lookup selects the app's member from the environment's active
  release set when one exists; it does not pick a partially prepared or newer
  deployment outside the active graph. Environments without a release set use
  the existing scoped-live-deployment behavior.
- A scoped domain is never an app's default domain. Environment clones do not
  copy domain claims: DNS ownership and TLS are external and must be configured
  deliberately for each environment.
- The environment diff reports scoped domains by workload and keeps them
  separate from shared application-wide domains.

## Consequences

The existing DNS challenge and certificate lifecycle remain unchanged. A
verified environment domain follows that environment's active deployment on
each request; it is deliberately uncached so promotion and environment
deletion take effect even if a notification is delayed.

## Follow-up: scoped edge policy (ADR-375)

An explicitly environment-scoped domain also uses that environment's workload
headers/CORS replacement and app-filtered edge policy. A missing policy keeps
that app's fallback; a present empty policy suppresses its headers/CORS.
Ordinary application-wide domains retain their existing edge-policy ownership.
The public compiler resolves the actual hostname binding before reading the
overlay in the same authoritative snapshot, so a shadowed domain cannot change
the policy of a higher-priority route. Publication and later policy mutations
validate bounded raw and compiled aggregates for these scoped domains.
