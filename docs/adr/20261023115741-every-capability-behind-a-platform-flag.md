# Every capability behind a platform flag

Date: 2026-10-23
Status: Accepted
Amends: ADR-1700 (platform flags), ADR-998 (capability catalog runtime availability)
Related: ADR 20261021080640 (flag subjects and previews), ADR-982 (apid domain packages)

## Context

Gregale has 168 customer-reachable capabilities
([inventory](../ga-capability-inventory.md)), but the product catalog listed
53 and only 19 had a platform flag. Everything else was reachable by every
account the moment it merged. To ship a small v1.0, the owner tiered every
capability (GA, beta, preview, internal) and asked for a flag on every
feature, so any capability can be released to selected accounts, or switched
off everywhere, at runtime and without a deploy.

## Decision

1. **The catalog is the source of truth.** `pkg/productcap/catalog.json` lists
   every customer capability. `maturity` is its tier. New optional fields:
   - `core: true` marks platform-core capabilities (sign-in, API keys, apps,
     billing, GDPR, status page, docs, CLI, dashboard, OpenAPI spec,
     notification emails).
   - `routes` lists the apid paths the capability owns, wildcards written
     `{}`. An entry owns its path and everything below it; an entry starting
     with `=` owns only that exact path. The longest matching entry decides.
   Operator systems (internal CI, internal hosting, internal mail, admin,
   compute nodes, the platform OCI registry) stay outside the product catalog
   and keep their env gates; `TestInternalCIIsAbsentFromProductCatalog`
   still holds.
2. **One flag per capability.** `platformflags.Catalog()` keeps the
   hand-written flags and appends a generated flag (key = capability id with
   `-` → `_`) for every capability no hand-written flag claims. Hand-written
   capability-level flags now declare their `Capability`.
   `platformflags.CapabilityFlag` resolves either kind; `FlagForCapability`
   still returns only hand-written flags.
3. **Tier sets the default.** GA and beta: on for every account. Preview and
   internal: kill switch on, default off, so an operator can release them to
   accounts with rules. Internal flags cannot be offered self-serve. The
   defaults apply the moment the release lands; operators add allow rules
   for accounts that need a preview back.
4. **apid enforces it** (`pkg/apid/capgate`):
   - Before authentication, a matched route whose capability's kill switch is
     off answers exactly like an unknown route (`404 no such API route` under
     `/v1`, ServeMux's plain 404 elsewhere), and a method mismatch on it is a
     404, not a revealing 405.
   - After authentication (`authLimited`, `authVerifiedLimited`,
     `sessionAuth`), the flag is decided for the account, plan and
     organization before any app or organization resource is resolved.
   - The gate decides by the matched pattern, or by the request path for a
     subtree pattern such as `GET /dashboard/`.
   - Operator routes (`/v1/admin`, `/v1/internal`, `/v1/compute-nodes`) and
     health/metrics are never capability-gated.
   - A route no capability owns is released and counted in
     `apid_capability_unmapped_requests_total`; hidden requests are counted in
     `apid_capability_hidden_requests_total{capability,stage}`.
   - `GET /v1/capabilities` reports a capability as unavailable when its flag
     is off for the account.
   - Dashboard pages hide links to capabilities that are off (`Page.On`).
5. **Core flags need force.** Writing a core capability flag with its kill
   switch or default off is refused with 409 unless the request carries
   `force` (`gregalectl flags set|disable|default|rollback … --force`).
6. **New routes must be assigned.** `TestEveryApidRouteHasACapability` fails
   when a route registered on apid has no owner in the catalog.
7. **Sub-features.** A capability may name a `parent`. It owns longer route
   paths than its parent (the longest entry decides) and is released only
   while it and every ancestor are released. The large capabilities (events,
   realtime, workflows and jobs, issues, operations, object storage, managed
   Postgres, MCP hosting, AI gateway, durable entities, queues, environments,
   edge rules, alerts, billing, debug, custom domains, outbound webhooks) are
   split into 90 sub-features. The realtime and operations sub-flags that
   predate this ADR now release their sub-feature. Core and always-on
   capabilities cannot be sub-features.
8. **Always-on capabilities have no flag.** `always_on: true` marks a
   capability that cannot be switched off (Firecracker isolation,
   scale-to-zero, the CLI, the SDKs).
9. **Bulk writes.** `gregalectl flags enable|disable|allow|deny|revoke|default`
   take `--tier <tier>` (every flag of that tier) or `--capability <id>` (that
   capability and every sub-feature under it) in place of `--key`. Each flag is
   written separately with the version read, and refused writes are reported.
   `gregalectl flags list` shows each flag's tier, parent, core mark and
   enforcement (`routes`, `settings` and `daemons`, joined with `+`, or
   `handler` or `none`).
10. **Settings.** A capability also owns `settings`: app request fields
    (`app.<field>` for any non-zero value, `app.<field>=<value>` for one
    value), edge rule kinds (`edge_rule.<kind>`), function runtimes
    (`runtime.<runtime>`), gregale.yaml blocks and trigger kinds
    (`manifest.<key>`, `manifest.triggers.<kind>`) and wildcard domains
    (`domain.wildcard`). App create and update, edge rule creation, custom
    domain creation and every source deploy's manifest refuse a setting whose
    capability (or an ancestor) is off with `403 capability_not_released`,
    naming the setting and the capability. An app update that keeps a value
    the app already has is not a new setting. Go and binary runtimes, gRPC,
    scaling targets and schedules, and wildcard domains are sub-features of
    their parents, so their own tier applies. `TestEverySettingHasACapability`
    fails when an app field, edge rule kind, runtime or gregale.yaml key has no
    capability and no recorded reason to stay ungated.
11. **Daemons stop work outside apid.** A capability lists the daemons besides
    apid that decide its flag (`consumers`). They follow the full decision,
    tier defaults included, through the shared decider `pkg/capflags`, with
    the flags kept current by each daemon's runtime-config watcher. Each knows
    a different subject, so a flag takes only rules every consumer can match:
    schedd knows the account, plan and organization of an app's resource
    account; githubd and gatewayd-internal know only the account.
    - **schedd** holds background work while it is off: cron and delayed
      runs, async invocations, durable inbound webhooks, queue deliveries,
      event triggers, jobs, workflows and managed exclusive operations. Held
      work is deferred a minute at a time without spending a delivery attempt,
      and resumes intact when the flag is on.
    - **githubd** ignores a push while `github-deploys` is off, and a pull
      request event while `pr-previews` or `github-deploys` is off. Ignored
      events are acknowledged and not replayed later. Closing a pull request
      still starts its preview's teardown.
    - **gatewayd-internal** decides per request, for the account that owns the
      app or rule: it drops `cache`, `async`, `retry` and `circuit_breaker`
      edge rules, and stops traffic mirroring, session affinity and the wake
      page (a browser waits for the wake like an API client). Rules that
      restrict traffic (WAF, challenge, IP, JWT, geo, limits, maintenance,
      headers, routing) keep enforcing while `edge-rules` or
      `edge-protection` is off: a flag change must never open an app its owner
      locked down, so those flags hide the API and refuse new rules only.
    - **realtimed** has no flag source, so apid decides for it: an endpoint
      whose account has `realtime` off is withdrawn from realtimed on its next
      sync (its connections end, new ones find no endpoint), unless it carries
      live routes. Live routes already follow the `live_queries` flag.
    - **livequeryd** keeps consuming: pausing it would leave the database's
      replication slot retaining WAL until the customer's disk fills. Clients
      lose live queries because apid stops sending live routes.
    `FAAS_CAPABILITY_FLAGS_RELEASE_ALL` releases every generated flag in each
    of these daemons, as in apid.
12. **The CLI hides what apid hides.** A capability lists the gregale command
    paths it owns (`cli`: a command or a verb path such as
    `bucket lifecycle`; the longest owned path decides).
    `GET /v1/capabilities` returns `hidden_capabilities`, every catalog
    capability (internal ones included) whose flag is off for the account.
    The CLI keeps it in the credential-bound completion cache for an hour.
    Help, completion, man pages and suggestions leave out hidden commands and
    verbs; logged out, or before the first read, they follow the tier
    defaults. Running a command is refused as an unknown command only when
    the cached view hides it and a fresh read agrees; an offline or logged-out
    CLI runs it and apid answers. Hiding is discovery, not enforcement: an
    older CLI shows everything, and apid still refuses the API calls. The
    generated CLI reference keeps every released command.
    `TestEveryCustomerCommandHasACapability` fails when a customer command has
    no capability and no recorded reason to stay visible, or a capability
    owns a path the manifest lacks.

13. **Published specs and SDKs follow the catalog.**
    - The anonymous `GET /v1/openapi.{json,yaml}` and `/docs` publish what
      every account gets: operator routes and the paths of capabilities not
      released by default (kill switch or default off, for the capability or
      an ancestor) are left out, with the components only they referenced.
      The served variant is recomputed when the flags change.
    - `sdk/openapi.yaml` (`make sdk-spec`, `TestSDKSpecInSync`) is
      `api/openapi.yaml` without operator routes, API monetization and the
      paths of preview and internal capabilities by tier. The SDKs are split
      on it: each default import exposes generally available and beta
      capabilities, and a preview entry exposes everything:
      `@gregale/sdk-node` and `@gregale/sdk-node/preview` (preview subpaths
      under `./preview/...`), `faas_sdk` and `faas_sdk.preview`, and Go's
      `faas` and `faas/preview` (`preview.Client` adds the methods on
      `api.PreviewClient`, and the package holds the helpers for preview
      capabilities). Preview code stays in the
      repository, and starters, examples and test suites import it from the
      preview entry. `api/openapi.yaml` stays complete for apid, the CLI and
      the contract gates.
14. **Nothing new ships without a capability.** ci-light runs
    `make capability-coverage-check`: `scripts/ga/capability_coverage.py`
    enumerates every customer entry point from the code (routes, CLI
    commands, manifest keys, plan limits, dashboard pages, docs, OpenAPI tags,
    daemons, flags, feature env flags, catalog ids, Go SDK files including
    `faas/preview`, tables, packages, deploy env, gateway files) and fails on
    any identifier no rule maps to an inventory row or an explicit
    non-capability class, on an inventory row with no entry point, and on a
    source that collects nothing. The same step runs `capabilities-check` and
    `sdk-spec-check`, so the catalog's generated matrix and the SDK spec
    cannot drift from it.

## Consequences

- Preview and internal capabilities disappear for every account on deploy.
  Beta users who need one back get an allow rule. Before rollout, list the
  accounts that used each preview capability in the last 30 days.
- Native acceptance environments that exercise preview capabilities through
  production wiring must enable the matching flags for their test accounts.
- No migration: flags live in `runtime_config_entries`. Older daemons ignore
  the new keys, so release N runs beside N-1.
- A capability turned off in schedd keeps its queue: a long-off capability's
  deferred work runs when the flag returns, which may surprise an account
  that forgot it. Operators turning a capability off for good should also
  have the work cancelled.
- Hand-written per-command usage strings can still name a hidden verb; the
  manifest-driven surfaces do not.
- Making the published surfaces follow the catalog exposed route-ownership
  gaps: org-scoped bucket and queue routes, account usage routes, MCP
  readiness alert routes and inbound-webhook bindings were owned by GA or
  beta prefixes, so the apid gate served those preview APIs to every
  account. They now belong to their capabilities.
