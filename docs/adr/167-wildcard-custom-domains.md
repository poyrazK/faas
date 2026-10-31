# ADR-167 · Wildcard custom domains

- **Status:** accepted
- **Date:** 2026-09-07
- **Decision:** Pro and Scale accounts may attach one or more customer-owned
  wildcard custom domains in the canonical form `*.example.com`. Ownership is
  proved with the existing DNS-01 TXT challenge at
  `_faas-verify.example.com`; certificate issuance uses the existing Hetzner
  or Cloud DNS solver and requests the wildcard name. A request for
  `a.b.example.com` resolves to the most-specific verified wildcard suffix;
  the apex `example.com` is not covered and must be attached separately.
  Exact custom-domain and verified tenant-surface routes take precedence over
  wildcard resolution.
- **Why:** A wildcard is the smallest customer-facing primitive for SaaS
  subdomains: one DNS delegation and one certificate can serve an unbounded
  set of app-owned labels without creating a tenant-hostname row per customer.
  DNS-01 is required because ACME HTTP-01 cannot validate a wildcard name.
- **Tenant-surface interaction:** A wildcard create is rejected with a typed
  409 when a non-deleted tenant surface already reserves any hostname below
  its suffix. Adding a tenant hostname below an existing verified wildcard is
  rejected with the same conflict. This keeps ADR-100's exact-host routing
  unambiguous; deleted surfaces do not block reuse.
- This ADR does not enable ADR-100's `tenant_surfaces.cert_kind=shared_wildcard`;
  that surface-level certificate grouping remains a separate follow-up.
- **Consequences:** Wildcard rows use the existing `custom_domains` table and
  cert lifecycle fields; the leading `*.` is an explicit name discriminator,
  so no migration is needed. DNS doctor probes use a deterministic concrete
  label (`www.<suffix>`), while the customer-visible TXT instruction remains
  at the zone owner name. Free and Hobby retain exact custom domains and
  tenant surfaces only.
- **Rejected alternatives:** Treating a wildcard as a tenant-surface
  hostname would inherit ADR-100's bounded hostname list and could not route
  arbitrary labels. Routing wildcard rows before exact routes would let a
  broad suffix shadow a deliberate tenant surface. Proving the literal
  `_faas-verify.*.<suffix>` owner is not a valid DNS-01 challenge name.

## Amendment (2026-10-20): unproved claims cannot take a verified wildcard's names

Creating a custom domain needs no DNS proof, and an expired, unverified row
was never deleted. Routing and the on-demand TLS check let any such row
shadow a verified wildcard above it. So one request from any account could
leave `api.example.com` dark for the account that had proved
`*.example.com`.

- A row that is unverified and past `verification_expires_at` reserves
  nothing (`CustomDomain.ClaimLive`). Exact lookups in routing and on-demand
  TLS ignore it, and `WildcardDomainForHost` skips it. The tenant-hostname
  overlap check therefore ignores it too.
- `POST /v1/domains` refuses, with `409`, an exact or wildcard name below a
  verified wildcard owned by another account (`state.ForeignVerifiedWildcard`).
  This is the same rule as the tenant-hostname conflict above. The wildcard's
  own account may still add exact names below it, which take precedence once
  verified.
- A live pending row still shadows the broader wildcard, as before. A claim
  made by another account before the wildcard verified can therefore darken
  that name until its 7-day deadline. After that it cannot be renewed.

## Amendment 2 (2026-10-31): a company's wildcard covers every company app

Bug hunt 32 (M5). `ForeignVerifiedWildcard` compared the account that created
the wildcard's app with the account that created the app asking for a name.
A company app keeps its creator's account as `account_id`
(ADR-202610171904), so a member could not attach a name below the company's
own verified wildcard to a company app they created: `409`, "belongs to
another account". Once the wildcard's creator left the company, the only apps
that could take new names under it were the ones that creator had made.

- Names below a verified wildcard belong to its tenant (`state.AppTenantID`):
  the app's shared organization, else the account that created it (an app
  with no organization, or one in its creator's personal organization). Any
  company app may take a name below a company wildcard. The creator's
  personal apps no longer can; they are another tenant.
- Managing a company app's domains already follows the organization's roles
  (ADR-20261026151103 amendment 4). This makes adding names under the
  company's zone follow them too.
- The claim takeover by DNS proof is unchanged. It still lets a member move
  another account's unverified claim, including a colleague's, onto their own
  app by publishing the takeover record.
