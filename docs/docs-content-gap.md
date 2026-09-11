# Documentation content gap

RFC 7807 problems the platform emits carry a `docs_url`. Those links now point
at the live documentation host (`https://gregale.dev/docs`) instead of the
never-deployed `docs.gregale.dev`.

The source-side customer pages and route catalog now live in this repository:
`docs/customer-pages.json` maps every customer-facing topic to a Markdown
source, and `make docs-links-check` rejects a new documentation URL unless it
is catalogued. The catalog deliberately aliases overlapping spellings such as
`/build` and `/builds`, while operator-only paths remain explicitly marked
external.

## Why HTTP checks alone cannot catch this

The docs site is a SPA. It answers **HTTP 200 for every path** — including
`/docs/zzz-not-a-real-page` — and renders its 404 in JavaScript. `curl`, a
link checker, and any CI gate all see a healthy 200 with a byte-identical
response body. Only a browser that executes JS can tell a real page from a
missing one.

Practical consequence: adding a link to a nonexistent SPA page is invisible to
HTTP-only checks. The source-side catalog is the deterministic contract that
CI can enforce before the docs site is published.

## Customer pages, by how often the API links to them

| Page | Referenced | Source-side contract |
|---|---:|---|
| `/plans` | 44 | `docs/plans.md`, generated from `pkg/api/limits.go`. |
| `/apps` | 17 | `docs/apps.md`; lifecycle + per-app settings. |
| `/deploys` | 14 | `docs/deploys.md`; `/deployments` is an alias. |
| `/orgs` | 12 | `docs/account.md`; org/team commands are included. |
| `/auth` | 9 | `docs/auth.md`; child paths use the same guide. |
| `/errors` | 8 | `docs/errors.md`; dynamic error codes use the prefix route. |
| `/jobs` | 6 | `docs/jobs.md`; run-to-completion workloads. |
| `/event-driven` | 6 | `docs/event-driven.md`; async invoke and triggers. |
| `/env` | 6 | `docs/env.md`; pull/push and scope guidance. |
| `/build` | 6 | `docs/build.md`; source-ref and limits are aliases. |
| `/domains` | 5 | `docs/domains.md`; verify and doctor are child routes. |
| `/secrets` | 4 | `docs/secrets.md`; sealed secrets. |
| `/registry-credentials` | 4 | `docs/registry-credentials.md`; private registry auth. |
| `/admin` | 4 | Operator-facing; marked external in the catalog. |
| `/sidecars` | 3 | `docs/sidecars.md`; stateless sidecar contract. |
| `/builds` | 3 | Alias of `/build`. |
| `/alerts` | 3 | `docs/alerts.md`; presets are a child route. |
| `/billing` | 2 | `docs/billing.md`; provider details use the same guide. |
| `/static-egress-ip` | 1 | `docs/static-egress-ip.md`. |
| `/postgres` | 1 | Existing `docs/managed-postgres.md`. |
| `/functions` | 1 | `docs/functions.md`. |
| `/dev` | 1 | Existing `docs/gregale-dev.md`; source-sync is a child route. |
| `/deploy-overrides` | 1 | `docs/deploy-overrides.md`. |
| `/crons` | 1 | Alias of `docs/event-driven.md`. |
| `/account` | 1 | `docs/account.md`. |

## Cleanup worth doing alongside

- Keep `docs/customer-pages.json` as the place where aliases are recorded;
  do not create a second page merely to satisfy a spelling variant.
- When a page is published by the docs site, nothing in the API needs to
  change — the link already points at the catalogued URL.

## Still on the legacy host

Repointing covered the customer-facing surface that composes against
`pkg/api`'s `docsBase`. Roughly 30 operator-facing links still build from
`wire.DocsHost` and remain dead:

- `pkg/vmmdgrpc/{server,proto,migration_handlers}.go` — `/vmmd#*` anchors on
  internal daemon-to-daemon gRPC problems
- `cmd/gregalectl/{main,output}.go` — operator CLI

These are a follow-up. There is no `/docs/vmmd` page either, so repointing them
without writing that page only changes which 404 an operator sees.
