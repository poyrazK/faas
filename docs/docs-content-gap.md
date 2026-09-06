# Documentation content gap

RFC 7807 problems the platform emits carry a `docs_url`. Those links now point
at the live documentation host (`https://gregale.dev/docs`) instead of the
never-deployed `docs.gregale.dev`. The **host** is fixed; the **content** is
not.

Of the 27 topics `pkg/api/errors.go` links to, exactly one — `/storage` — is
among the 14 guides the site publishes. The rest render the site's client-side
404 until the pages are written.

## Why CI cannot catch this

The docs site is a SPA. It answers **HTTP 200 for every path** — including
`/docs/zzz-not-a-real-page` — and renders its 404 in JavaScript. `curl`, a
link checker, and any CI gate all see a healthy 200 with a byte-identical
response body. Only a browser that executes JS can tell a real page from a
missing one.

Practical consequence: adding a link to a nonexistent page is invisible to
every automated check we have. This list is maintained by hand.

## Pages to write, by how often the API links to them

| Page | Referenced | Notes |
|---|---:|---|
| `/plans` | 44 | Every plan-limit problem. Highest-value page by a wide margin. |
| `/apps` | 17 | App lifecycle + per-app settings. |
| `/deploys` | 14 | Deploy pipeline and failure classes. |
| `/orgs` | 12 | Org / team model (IAM-6, ADR-061). |
| `/auth` | 9 | Includes `/auth/reset`, `/auth/sign-in`, `/auth/oauth`, `/auth/email-verification`. |
| `/errors` | 8 | The per-code explanation pages the whycopy catalog references. |
| `/jobs` | 6 | Run-to-completion jobs. |
| `/event-driven` | 6 | Async invoke, delayed tasks, long-poll. |
| `/env` | 6 | Environment variables. |
| `/build` | 6 | Includes `/build/source-ref`, `/build/limits`, `/build/source`. |
| `/domains` | 5 | Includes `/domains/verify`, `/domains/doctor`. |
| `/secrets` | 4 | Sealed secrets. |
| `/registry-credentials` | 4 | Private registry auth. |
| `/admin` | 4 | `/admin/compute-nodes` — operator-facing. |
| `/sidecars` | 3 | |
| `/builds` | 3 | Distinct from `/build`; worth collapsing into one page. |
| `/alerts` | 3 | Includes `/alerts/presets`. |
| `/billing` | 2 | Includes `/billing/providers`. |
| `/static-egress-ip` | 1 | |
| `/postgres` | 1 | Managed Postgres. |
| `/functions` | 1 | |
| `/dev` | 1 | `/dev/source-sync` — the `gregale dev` loop. |
| `/deployments` | 1 | Overlaps `/deploys`; pick one. |
| `/deploy-overrides` | 1 | |
| `/crons` | 1 | |
| `/account` | 1 | |

## Cleanup worth doing alongside

- `/build` vs `/builds` and `/deploys` vs `/deployments` are the same topics
  under two spellings. Collapse each pair before writing the pages, or the
  split will be baked into the URLs.
- When a page ships, nothing in the code needs to change — the link already
  points at the right URL.

## Still on the legacy host

Repointing covered the customer-facing surface that composes against
`pkg/api`'s `docsBase`. Roughly 30 operator-facing links still build from
`wire.DocsHost` and remain dead:

- `pkg/vmmdgrpc/{server,proto,migration_handlers}.go` — `/vmmd#*` anchors on
  internal daemon-to-daemon gRPC problems
- `cmd/gregalectl/{main,output}.go` — operator CLI

These are a follow-up. There is no `/docs/vmmd` page either, so repointing them
without writing that page only changes which 404 an operator sees.
