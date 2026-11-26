# ADR-20261125232048: Custom Postgres roles reach beta

Status: Accepted
Amends: ADR-20261116213000 (custom Postgres group roles; was Proposed, dark behind `postgres_custom_roles`)
Related: ADR-20261023115741 (tier sets the flag default), ADR-20261123112326 (runtime roles run the owner's functions)

## Context

ADR-20261116213000 shipped custom roles dark: the `postgres-custom-roles`
capability was preview, its platform flag defaulted off, the CLI hid
`gregale postgres roles` unless `FAAS_POSTGRES_CUSTOM_ROLES_ENABLED` was set,
and the routes stayed out of the OpenAPI spec and SDKs.

Its acceptance case now holds on production. With only Gregale-issued
credentials, Windmill 1.875 on a Neon-backed managed database (2026-11-23
and 2026-11-25, rc.381):

- runs its migrations in a release command on the `migration` login, roles
  and grants included;
- serves on `read_write` logins that start as `windmill_admin`
  (amendment 2), and switches to `windmill_user` per request;
- logs in, creates workspaces and scripts, and runs Bun scripts, one with an
  npm dependency, on its worker.

Getting there fixed two platform gaps (amendment 1 `TEMPORARY`;
ADR-20261123112326 function `EXECUTE`). The rest was Windmill configuration,
now in the guide.

## Decision

- `postgres-custom-roles` is **beta** on Hobby, Pro and Scale. By
  ADR-20261023115741 beta is on for every entitled account; the hand-written
  `postgres_custom_roles` flag declares that default
  (`Enabled: true, Default: true`) so a fresh installation agrees with the
  tier. An installation whose operators already wrote the flag keeps their
  value: production sets its default with
  `gregalectl flags default --key postgres_custom_roles --value true --reason ... --yes` after the release.
- The CLI shows `gregale postgres roles` like any other released command; the
  environment switch is gone. Discovery still follows the account's
  capability view.
- The three routes are in `api/openapi.yaml`, the public spec and the SDKs.
- `/docs/postgres-custom-roles` is published, with the Windmill guide.

## Consequences

- Any Hobby+ account can declare up to 16 roles per database, including
  `BYPASSRLS` ones on databases without Data API or Auth.
- The Free plan still answers `plan_feature_gated` on writes.
- A journey (`tests/journeys`) covers the customer path: declare, grant,
  default role, SET ROLE from a deployed app, and delete.
