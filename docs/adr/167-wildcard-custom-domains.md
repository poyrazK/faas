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
