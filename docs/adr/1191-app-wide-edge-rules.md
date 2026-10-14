# ADR-1191: App-wide edge rules (`match_host: "@app"`)

- **Status:** accepted
- **Date:** 2026-10-14
- **Related:** ADR-091 (edge rules), ADR-520 (custom-domain TLS),
  ADR-831 (edge protection), ADR-986 (protection recipes)

## Context

Every edge rule matches one `match_host`: an exact host or a glob. The
dashboard presets and protection recipes attached their rules to the app's
platform host (`<slug>.gregale.dev`). Platform hosts sit behind Cloudflare;
custom domains reach the platform's own edge directly (ADR-520/831), so the
hosts that most need edge protection were the ones a one-click preset did
not cover, with nothing on the page saying so. Covering them meant one rule
per domain, repeated whenever a domain was added.

A `*` rule is not a substitute: it is filtered only by account (the gateway's
owner check), so it would apply to every app of the account.

## Decision

1. **`@app` is a reserved `match_host`** meaning every hostname the rule's
   own app serves: its platform host and its custom domains, including
   domains added later. Other values starting with `@` are rejected so the
   prefix stays reserved.

2. **The gateway attaches `@app` rules by app, not by host.** The request
   handler records the resolved app next to the rule owner; the per-host
   loader merges that app's enabled `@app` rules into the host's rule set
   in the normal evaluation order (priority, created_at, id). The compiled
   set is cached per host *and* app, so a host that moves to another app
   never reuses the old app's rules. Host resolution (custom domains,
   environment routes, platform tenants) stays in one place — the router.

3. **Convergence.** An `@app` mutation invalidates every cached host (as
   `*` does) and its fence holds only hosts an app of the owning account
   serves — never an unclaimed host or another account's.

4. **Defaults.** The security-headers preset and protection recipes create
   `@app` rules; the CLI's `edge-rules recipe` restricts to one host only
   with `--host`. Hand-written rules keep whatever host they are given.

5. **Visibility.** The edge-rules page lists every hostname the app serves
   and how many enabled rules apply to each (exact, glob and `@app` alike);
   a custom domain no rule covers is flagged. App-wide rules show as "every
   app host" in the rules table. The trace simulator treats `@app` as
   matching the simulated host, since a trace is already scoped to one app.

## Consequences

- One rule protects every domain of an app, including future ones.
- The gateway does one extra indexed query per (host, app) cache fill.
- MCP resource policies still require their canonical host.
