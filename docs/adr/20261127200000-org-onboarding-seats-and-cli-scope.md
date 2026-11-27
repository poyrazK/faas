# ADR-20261127200000 · Organizations: no-seat plans refuse invitations, and `--org` acts for the organization

- **Status:** accepted
- **Date:** 2026-11-27
- **Scope:** `pkg/apid/apidserver` (invite and accept), `pkg/api` (client routing, errors), `cmd/gregale` (`--org`, `billing plan`), dashboard org page, `docs/account.md`
- **Amends:** ADR-061 (organizations; plan caps), ADR-20261113090000 (company projects from the CLI)

## Context

A review of the organization feature on 2026-11-27 found that a team could
not get started from the terminal:

1. A new shared organization starts on Free, whose plan row has
   `OrgMembersMax = 0` and `OrgPendingInvitationsMax = 0`. The invite handler
   read a pending cap of 0 as "no cap" and minted the invitation; every accept
   then failed with `org_member_cap_exceeded` and a "0 of 0" detail.
2. The CLI and MCP had no way to change an organization's plan or open its
   billing portal. `gregale billing plan` and `billing portal` always acted on
   the personal account, and the dashboard's billing page is personal too.
3. The global `--org` flag rewrote only `/v1/projects` paths.
   `gregale --org acme deploy`, `add postgres` or `billing plan` ran against
   the personal account with no warning.
4. The dashboard's org page told people to run `gregale orgs invite SLUG EMAIL`,
   which does not exist.
5. Listing members read each member's account one by one.

## Decision

1. **A plan with no seats refuses invitations.** When the organization's plan
   has `OrgMembersMax <= 0` or `OrgPendingInvitationsMax <= 0`,
   `POST /v1/orgs/{slug}/members` answers `403 org_invitation_cap_exceeded`
   with the title "Organization plan has no member seats" and a fix line
   (`gregale --org SLUG billing plan hobby`). No invitation is stored. The
   code stays the same, so clients that already branch on it keep working.
2. **An accept on a plan with no seats asks for an upgrade.** An invitation
   minted before this change, or before a downgrade, still fails at accept
   with `403 org_member_cap_exceeded`. The detail now tells the invitee to ask
   the owner to upgrade. A plan that has seats but is full reports its real
   limit instead of 0.
3. **`--org` selects the organization for every call that has an
   organization form.** With `--org SLUG`, the client sends:
   - `POST /v1/apps` to `POST /v1/orgs/SLUG/apps`, so a first deploy creates
     the app in the organization;
   - `POST /v1/apps/{app}/buckets` to the organization's bucket route;
   - plan, billing status, billing portal, costs, budgets, overage cap and
     egress overage to the organization's billing routes;
   - managed PostgreSQL paths to `/v1/orgs/SLUG/postgres/...`, except the
     operations that have no organization form yet (query, roles, live
     connections, cutovers and a few settings), which keep their flat path.
   App-scoped paths are unchanged: app slugs are global, and the server
   authorizes the member's role on a company app (ADR-202610171901).
4. **`--org` never quietly acts on or shows the personal account.** The
   monthly usage read (`GET /v1/usage`) goes to the organization's usage.
   Under `--org`, these fail in the client with `unsupported_by_cli` before
   they are sent:
   - personal API keys (`/v1/keys`): the error points to
     `gregale org key ... --org SLUG`, because organization keys answer in
     other shapes;
   - billing and usage calls with no organization form (forecast, FOCUS
     export, daily and storage usage, budget alert channels, cancel, retry);
   - the personal usage reads and the account export under `/v1/account`,
     and the managed PostgreSQL overage setting;
   - any other write to `/v1/account`, including account deletion.

   The client's hand-built requests (the streamed account export and account
   deletion) go through the same routing as `do`.

   Other `GET /v1/account...` reads, such as who-am-I and rate limits, still
   work.
5. `gregale --org SLUG billing plan PLAN` reads the organization's plan for
   its downgrade confirmation. Its retry and keep-subscription hints keep
   `--org`, and it points to `gregale org info SLUG` to check the switch.
6. Source-scanning tests pin the client mapping to apid's registered routes:
   - every rewritten path must exist;
   - every PostgreSQL path left flat must have no organization form, so a new
     organization route is not silently skipped;
   - a mapped route must reach the same handler as the personal one, unless
     it is a reviewed exception. Only the plan route answers a different
     shape (`OrgPlanResponse`), and the CLI calls `ChangeOrgPlan` for it, so
     `--json` prints the organization's result.

   The seat decisions live in `pkg/apid/orgseats`.
7. The dashboard hint names `gregale org members invite --org SLUG --email
   ADDR`, and the member list reads accounts in one batch (`AccountsByIDs`).

## Consequences

- On Free, inviting fails at once and names the upgrade, so it no longer looks
  like it worked. Free organizations can no longer pile up invitations that
  can never be accepted.
- Scripts that used `--org` with non-project commands and expected the
  personal account now act on the organization, or fail with
  `unsupported_by_cli`. That earlier behaviour was the defect.
- Invitations are still not emailed: the inviter shares the token. Emailing
  invitations is a separate product and abuse decision and is not made here.
- Moving company apps from the creator's account namespace to the
  organization (ADR-20261029100110, ADR-20261025211616) is unchanged.
