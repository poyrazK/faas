# ADR-20261112114642: Direct entry stays off the platform domain

- Status: accepted
- Date: 2026-11-12
- Amends: ADR-20261103163954 (app hosts point straight at the nodes),
  ADR-20261026100000 (platform and app domains), ADR-20261107180000
  (edge security round 8, R8-6)

## Context

R8-6 (ADR-20261107180000, left open): direct entry gives every compute node
the key of a browser-trusted certificate for the names edgednsd orders. Today
apps and the platform share gregale.dev, so that certificate is
`*.gregale.dev`, and it also covers `api.`, `s3.`, `operations.` and the
other platform hosts. A compromised compute node could answer for the
dashboard and API with a certificate every browser accepts.

ADR-20261026100000 already moves apps to their own domain (gregale.app) by
configuration alone, keeping gregale.dev as the platform domain and as an
alias for old app URLs. The domain is not bought yet.

## Decision

1. **Once the platform domain differs from the apps domain, direct entry may
   name nothing under the platform domain.** Manifest validation refuses a
   `public_edge.dns.certificate` name under the platform domain (its apex, a
   host or a wildcard) and a DNS-only `public_edge.dns.names` entry under it.
   gregale.dev app hosts stay reachable through the alias on the proxied
   control-plane path, whose Origin CA certificate only Cloudflare trusts.
   While the two domains are one (the pilot) nothing is refused: the split
   is not possible yet, and the risk stays recorded here until the move.
2. **meterd receives the apps domain** (`99-faas-apps-domain.conf`). It
   probes customers' synthetic checks at `<slug>.<apps domain>` and fell back
   to gregale.dev, which redirect mode answers with a 308.
3. **The move is a runbook** (`docs/runbooks/AppsDomainMove.md`), not code:
   zone, certificates, Public Suffix List, manifest, rollout, redirect,
   rollback. A render test pins the target manifest.

## Consequences

- No behaviour changes until the manifest sets `platform_domain` apart from
  `apps_domain`.
- edgednsd reconciles only the names it manages, so the gregale.dev direct
  records (the pilot's) must be removed by hand at the move; the runbook says
  how.
- The CLI keeps `gregale.dev` only as an offline fallback; it prints the
  URLs the API returns, which follow the apps domain.
- The `__Host-` session cookie prefix (ADR-20261026100000 step 4) and the
  Public Suffix List entry (its amendment 1) stay separate steps.
