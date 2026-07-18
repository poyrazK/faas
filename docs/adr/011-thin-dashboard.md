# ADR-011 · Thin dashboard at launch

- **Status:** accepted
- **Date:** 2026-07-17
- **Decision:** Ship a *thin* server-rendered dashboard inside `apid` at launch (was gap G3, moved from post-M8 to M7.5). Go `html/template` + HTMX, no SPA build chain, fits the 6 GB control-plane slice (spec §13). The dashboard routes (`/dashboard/*`, `/login`, `/auth/verify`, `/oauth/*`) are reverse-proxied from `gatewayd` to `apid`'s loopback listener — `apid` stays loopback-only, `gatewayd` remains the single public listener (§11).
- **Why:** the GitHub connect-repo funnel (UX spec §5) requires an OAuth callback and a repo picker. A CLI-only launch strands the git-deploy path the founder chose (UX spec §11 line 272: *"do not implement git-deploy without landing ADR-011 and ADR-012 first"*). The dashboard is the OAuth state machine's natural host — colocating the callback handler, the repo picker, and the per-account surface keeps the state where the read API lives.
- **Consequences:**
  - `apid`'s mux grows dashboard routes under `/dashboard/*` and `/oauth/*`. The existing rule in `cmd/apid/server.go` (doc comment: *"New routes append here; do not introduce per-feature sub-muxes"*) stays.
  - `gatewayd` becomes the proxy for `/dashboard/*` and `/oauth/*` (and `/webhooks/github` — see ADR-012). Adds a `httputil.ReverseProxy` segment to `cmd/gatewayd/main.go`'s `runWithDeps`.
  - Magic-link mailer is a hard dependency (gap G4 closes in this slice). No password store, no JWT-with-embedded-PII — session cookies sealed with a host-side key (gap G2 sealed-at-rest).
  - §11 gaps close in the dashboard work: panic recovery, request-ID, auth-failure rate limit (the auth gate gets a 10/min/IP token-bucket keyed on 401 responses, per spec §11 line 398).
  - apid is loopback-only; the only TLS termination lives on `gatewayd` (CertMagic + the existing wildcard cert from ADR-007). No public listener is added to apid.
  - A slice of dashboard work lands at M7.5 instead of post-M8 (UX spec §12 milestone table).
- **Rejected alternatives:**
  - **Keep CLI-only at launch, defer dashboard to M8.** Rejected: breaks §5 connect-repo UX and strands the git-deploy funnel the founder chose.
  - **Ship a SPA (React / Vue).** Rejected: adds a node build chain to a one-box platform, blows the 6 GB control-plane slice (spec §13), and increases the dashboard's attack surface (XSS via SPA hydration) — server-rendered HTML has a much smaller trust boundary.
  - **Static dashboard on a CDN with apid holding the OAuth state.** Rejected: the OAuth callback needs server state (state cookie, code-exchange) — putting the dashboard outside the server's reach re-introduces the SPA's deployment friction for zero gain.
  - **Expose apid on a second public listener.** Rejected: violates the §11 "single public listener" invariant. gatewayd already has CertMagic + the canonical request-ID injection + rate-limit observability — splitting the public surface doubles the §11 attack surface.

## Re-evaluation triggers

- **M8 hardening (§11 checklist):** if §11 fuzzing finds dashboard-side XSS that server-rendered HTML can't mitigate, the answer is to add a strict CSP header + per-session CSRF token, NOT to migrate to a SPA. SPA is rejected for the duration of v1.
- **Gate-A multi-host (spec §16):** the dashboard reverse-proxy currently lives in gatewayd. At Gate-A we may split gatewayd into gatewayd-public + gatewayd-internal — the dashboard proxy segment moves to internal, the public listener gets only TLS termination. No spec change here; ADR-018 (gRPC surface) is the relevant precedent.