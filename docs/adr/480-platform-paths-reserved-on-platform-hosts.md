# ADR-480 · Reserve platform paths on platform hosts only

- **Status:** proposed
- **Date:** 2026-10-03
- **Amends:** spec §4.1.1 (platform path reservations). ADR-011's single
  listener is unchanged; only the Host scope of the reservation moves.
- **Decision:** The paths matched by `apid.IsApidPath` (`/v1`, `/dashboard`,
  `/oauth/*`, `/login`, `/signup`, `/login/forgot`, `/auth/verify`,
  `/auth/reset`, `/logout`, `/status`, `/cli-auth`, `/docs`) are routed to
  apid only when the request Host is a platform host: the apps-domain apex,
  `api.<apps domain>`, `operations.<apps domain>`, or a loopback/IP probe
  (`apid.IsPlatformHost`). On every other Host (app subdomains, preview
  hosts, custom domains, tenant surfaces) they reach the app. Both routers
  apply the rule: gatewayd-public's control-plane proxy and gatewayd-internal's
  apid proxy. The edge's `/.well-known/security.txt` and
  `/.well-known/oauth-authorization-server` documents describe Gregale and
  are scoped the same way.
- **Why:** The every-host reservation predates app subdomains and custom
  domains. The production-us end-to-end run (2026-09-30, re-checked on rc.231)
  showed customer routes shadowed on `https://<app>.gregale.dev`: `/status`
  returned the platform's HTML status page (the app's 503 was never seen),
  `/v1/users` returned apid's 404 problem, `/docs` served the platform API
  docs, and `/login` and `/oauth/callback` redirected to gregale.dev. That
  breaks versioned APIs under `/v1`, FastAPI and Swagger `/docs`, app login
  pages, OAuth callbacks and `/status` endpoints — on custom domains too. An
  MCP server that is its own authorization server also could not publish its
  RFC 8414 metadata. The edge already redirected browser pages away from
  tenant hosts so that platform sessions were never issued on attacker
  origins; with this change apid never sees tenant-host traffic at all.
- **Consequences:**
  - Customer apps may serve every path on their own hosts except the
    remaining every-host reservations below. Platform hosts are unchanged.
  - Still reserved on every Host, because platform components address them
    through app hosts or they gate certificate issuance:
    `/v1/apps/{slug}/logs` (gatewayd-internal log stream),
    `/v1/synthesize`, `/v1/invocations:dispatch`,
    `/v1/invocations:dispatch_batch`, `/v1/internal/realtime/`,
    `/v1/traces/`, `/v1/otel/v1/traces` (ADR-127 customer span ingest) and
    `/.well-known/acme-challenge/`.
  - With no apps domain configured (dev single-box, the e2e harness) a router
    cannot tell an app host from a platform host and keeps the every-host
    reservation.
  - The platform API, dashboard, status page and docs are reachable only on
    platform hosts. Nothing in the repository addresses them through an app
    host: SDKs and the CLI use the API base URL, OAuth issuers are fixed to
    `https://api.gregale.dev`, and GitHub callbacks use the apex.
  - The edge's tenant-host browser-page redirect and cookie stripping for
    apid-bound requests become unreachable on tenant hosts; the stripping
    remains as defense in depth.
- **Rejected alternatives:**
  - A per-app opt-out of individual reserved paths: every app would need to
    discover the conflict first, and platform sessions would still be issued
    on tenant origins for apps that never opt out.
  - Moving the platform surfaces under a single reserved prefix
    (`/_gregale/`): changes every client URL and still shadows that prefix.
