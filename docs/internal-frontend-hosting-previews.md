# Private release previews (internal, default off)

A preview gives a verified release its own `https://p-UUID.HOSTING_DOMAIN` URL
before promotion. It remains pinned to that release when the production site
changes. It works before the site's first promotion. Previews require the
existing isolated hosting domain, wildcard DNS/TLS/Worker route, internal Access
application and durable R2 routing; they need no per-preview provider objects or
Enterprise custom metadata. This milestone does not provision or qualify live
Cloudflare resources.

## Opt-ins and upgrade

Keep all gates off during upgrade. Drain **all** apid writers/reconcilers, apply
`20261016195404519_hosting_private_previews.sql`, and upgrade every writer,
reconciler and Worker before restarting. The migration queues a new generation
for every existing durable head; new heads include explicit `preview_allowed`.
Old publishers must not consume those generations. Verify the route outbox has
published those generations before opting into previews. Older heads without the
permission fail closed for previews. Old garbage collectors must not run while
preview references exist: their SQL delete would be rejected by the FK only after
they had already removed bytes.

The existing main hosting/operator/MFA/scope/account gates remain required:

| Surface | Additional default-off opt-in |
| --- | --- |
| API runtime | `frontend_hosting_previews_enabled` or bootstrap `FAAS_FRONTEND_HOSTING_PREVIEWS_ENABLED=1`, plus durable routing admission |
| Worker | `FAAS_FRONTEND_HOSTING_PREVIEWS_ENABLED=true`, plus main and durable routing serving switches |
| Terraform | `previews_enabled = true`; generated apid environment still leaves admission at `0` |
| Dashboard build | `NEXT_PUBLIC_FRONTEND_HOSTING_PREVIEWS_ENABLED=true`, plus main dashboard flag; API capabilities must advertise support |
| CLI workstation | `FAAS_FRONTEND_HOSTING_PREVIEWS_ENABLED=1`, plus main workstation hosting flag |

No flags are enabled by this change. Configuration serving still has its own
independent gate. No new DNS/Access/certificate objects are created. Existing
wildcard Access must cover the `p-UUID` label. The Worker verifies the same
canonical internal audience and email allowlist on every preview request.

## Use

Stage and verify a release using the [release workflow](internal-frontend-hosting-releases.md).
Then create, inspect and revoke its preview:

```bash
export FAAS_FRONTEND_HOSTING_ENABLED=1
export FAAS_FRONTEND_HOSTING_PREVIEWS_ENABLED=1
gregale sites previews create --site SITE_UUID --release READY_RELEASE_UUID --ttl 24h
gregale sites previews list --site SITE_UUID
gregale sites previews revoke --site SITE_UUID --preview PREVIEW_UUID --expected-generation 1
```

The API exposes `GET/POST /v1/internal/hosting/sites/{site}/previews` and
`DELETE /v1/internal/hosting/sites/{site}/previews/{preview}`. Create accepts
`release_id` and optional integer `ttl_seconds` (zero/omitted uses the default).
Revoke requires `expected_generation: 1`; generations are not silently refreshed
or retried. Mutations return 202 accepted intent. List rows show
`published_generation`, `generation`, `published_at`, expiry and fixed error
codes. Check receipt equality and canonical publication before opening a URL;
publication acknowledgment is not proof of all-edge readiness. After an uncertain
write, list and review the result before retrying; a new create request can create
another distinct URL.

The dashboard's Private previews panel selects any ready release, accepts a
lifetime in minutes and waits for a publication receipt before displaying open
and copy controls. It uses the selected release directly, never a promotion API.
Failed/ambiguous writes block further writes until a successful refresh. Browser
expiry labels use the last refresh; the Worker enforces expiry on every request.

## Isolation and lifecycle

The binding is immutable: to change release or extend expiry, create a new URL.
A preview can serve only its release, including its redirects, headers, SPA and
custom 404 settings. It cannot use retained production assets or another release's
pinned path. Production/custom-domain routes cannot reach an unpromoted release
merely because it has a preview. Different preview labels provide separate browser
origins. They share internal Access; this is an operator pilot, not public or
customer-specific preview authentication.

Authentication, preview expiry/revocation and canonical permission checks precede
warm file cache, HEAD and 304 responses. Suspension/deletion deny previews after
canonical head refresh. A missing/invalid preview or canonical head never falls
back to KV or a production route. Release bytes remain private with browser
revalidation. Hard expiry applies without an API heartbeat. No system can revoke
bytes already downloaded by an authorized reviewer.

There are eight preview references/site, default TTL 24 hours, minimum 5 minutes,
maximum 72 hours, enforced in `pkg/api/limits.go` and advertised by capabilities.
Revoked or expired rows still count while cleanup is pending. Any reference blocks
release retirement; revoke and wait for cleanup first. Site deletion atomically
revokes previews and retires releases. The artifact collector checks references
before removing bytes, then SQL foreign keys protect catalog deletion as well.
Reconciliation continues with admission off. Fixed errors `publication_unavailable`
and `origin_unavailable` preserve pending intent and references.

After tombstone publication, the controller waits 150 seconds (two 60-second head
cache windows plus the 30-second storage timeout) before purging the SQL row.
Publication, edge refresh and cleanup are separate states. Revocation/suspension
can take up to 60 seconds after publication to affect cached heads; expiry itself
is checked each request. R2 tombstones under `hosting/previews/` survive catalog
purge and are never release-GC targets. Keep these objects for the pilot's lifetime
to fence delayed publications. Do not add a bucket lifecycle that deletes heads.

Disabling the API flag stops management but leaves expiry/revocation cleanup
running. The Worker flag stops preview serving immediately at request dispatch;
the main hosting Worker flag remains the broader serving kill switch. Before
downgrade, revoke all previews and wait for references to disappear, drain all
writers/reconcilers, then roll back; the migration refuses downgrade with rows.

## Resource cost and evidence

Release files are shared with the existing staged release; previews add no copy,
VM, per-site RAM or CPU allocation. Each reference adds one bounded SQL row/indexes
and a small R2 head (plus its retained tombstone). The edge caches preview heads
for at most 60 seconds and shares canonical file-cache keys. Active repairs run
at six-hour intervals, or at expiry; failures retry after 30 seconds. Dispatch is
bounded to 100 rows, two concurrent workers and 30 seconds/pass. Preview traffic
is excluded from production traffic observations; existing release storage
reservations remain charged until physical cleanup succeeds.

Local checks cover memory/Postgres ownership, concurrent quota, immutable expiry,
CAS revocation versus a late first publication, stale receipts, downgrade guards,
release GC safety, Worker warm/cold access/expiry/HEAD/304/isolation/configuration,
independent Terraform flags, CLI intent, and dashboard create/revoke flows with
mocked APIs. Provider Access wildcard coverage, real R2 conditional writes,
real DNS/TLS and all-edge behavior still require the deferred isolated live pilot.

The [release source and history guide](internal-frontend-hosting-history.md) explains optional caller-provided source labels and bounded accepted-promotion history, behind a separate default-off opt-in.
