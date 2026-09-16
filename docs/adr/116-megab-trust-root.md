# ADR-116 — Mega-B trust-root split: dashboard owns the install-token, CLI owns the deploy input

Status: Accepted, 2026-08-18
Issue: #961 (DEPLOY-UX leaves 5 + 6)
Closes: Mega-B PR-2 + PR-3

## Context

Issue #961 ("Frictionless deploy gap") closes the customer-facing
"deploy-from-the-dashboard" affordance. The CLI already ships
`gregale deploy --repo <owner>/<name>` (issue #739 / ADR-092, the
source-ref deploy) and `gregale deploy --template NAME`. Mega-B
adds the dashboard side: `gregale connect repo <owner>/<name>` (PR-1)
opens the dashboard's `/dashboard/apps/new?repo=…` URL in a browser;
the dashboard's wizard drives the OAuth handshake + install + bind
server-side.

The disagreement the plan surfaced is a §11 trust-root question:
the GitHub install row is anchored to a `github_installations`
record whose only proof of ownership is the cookie session's
`env.GithubLogin` (PR-B's `bindAppToRepo` re-runs `VerifyInstallation`
with the session's `github_login` as `expected_login`). The CLI
holds an API key — no cookie session, no `env.GithubLogin`. So the
two surfaces cannot share the bind endpoint without a §11
regression.

## Decision

The two surfaces own different trust roots:

1. **Dashboard is the install-token trust root.** Cookie +
   session-bound `env.GithubLogin` is the only thing that proves
   the GitHub identity bound to a `github_installations` row (per
   §11). The CLI holds an API key, not a session cookie; reusing
   the bind endpoint from the CLI would let an API key impersonate
   a GitHub identity it doesn't own.

2. **CLI is the source-of-truth root for `--template` and `--repo`
   (the deploy input).** The CLI pushes a deploy request into the
   v1 API; the dashboard handles the OAuth handshake server-side.
   `gregale connect repo <owner>/<repo>` is browser-driven and
   reuses the existing `startConnectGitHub` + `renderOAuthCodeCallback`
   paths. No new API-key-accepting bind endpoint. No session cookie
   minted from an API key.

Concretely:

- `gregale connect repo <owner>/<name>` (PR-1) opens
  `/dashboard/apps/new?repo=<owner>/<name>` in a browser. The CLI
  does NOT call `POST /v1/apps/{slug}/install/bind`.
- `/dashboard/apps/new` (PR-3) renders a 3-step wizard
  (Connect GitHub → pick install + repo + template → bind).
- The wizard's submit form POSTs to the dashboard-only
  `/dashboard/apps/new` adapter. It creates the app, verifies the selected
  repository against the installation, and then calls the existing durable
  binding primitive. The cookie-session §11 proof re-runs server-side.
- `GET /v1/templates` (PR-3) is the dashboard's source of truth for
  the template catalog. The CLI's runtime validator reads the same
  `templates.Names` locally (the embed FS in `cmd/gregale/templates/`).
  The two paths agree because they read the same source — release
  docs note the pairing requirement (a divergent CLI + apid release
  could show 13 names on one side and 14 on the other; the pin test
  in `commands_meta_test.go` catches drift within a single build,
  but the cross-binary drift needs coordinated releases).

## Consequences

### PR-2 — Template catalog widening (CLI manifest)

- The `ClosedSet` literals at `cli_meta.go:308` (deploy) + `:362` (init)
  widened from the stale `node22-http`/`python312-http` (neither
  exists in `templates.Names` or the embed FS) to the canonical
  13-name catalog.
- The 13 names lifted to a `templateNames13` var so goconst stops
  flagging the duplicated list (the literal-vs-const rule fires
  package-wide in golangci-lint v2.4.0).
- `commands_meta_test.go` adds two tests:
  `TestClosedSetTemplatesMatchEmbedFS` (ClosedSet ↔ templates.Names
  parity, both directions) and `TestTemplateNames13MirrorsEmbedFS`
  (order parity, byte-for-byte). The existing
  `TestCompletion_ManifestDrift` walks main.go's switch and asserts
  command-level parity; flag-level ClosedSet drift is a different
  audit, so a dedicated test is clearer than overloading it.
- The runtime validator at `commands_init.go:132` already uses
  `templates.Exists` → `templates.Names` and prints the right
  13-name list in its `unknown --template` error message. This PR
  is a parity fix — completion was advertising two templates that
  don't exist while hiding 13 that do.

### PR-3 — Dashboard wizard

- `GET /v1/templates` (handlers_templates.go) returns the 13-name
  catalog with category + description. Cookie-session-authenticated;
  no API-key access. Mirrors `templates.Names` without importing
  the CLI's main package.
- `/dashboard/apps/new` (handlers_dashboard_apps_new.go) renders
  three states: Connect-first (no `env.GithubLogin`), Degraded
  (githubd unreachable), Form (everything wired). The form POSTs to
  the dashboard-only create adapter, which performs the §11 proof,
  creates the app, verifies repository visibility, and persists the
  binding through githubd.
- New `peekSessionGithubLogin` helper (handlers_dashboard_apps_new.go)
  is the side-effect-free sibling of `sessionGithubLogin`. The
  wizard peeks the cookie so it can render the Connect CTA; the
  bind endpoint still re-runs the proof at write time.
- `pkg/dashboard/templates/apps_new.html` (new) renders the full
  HTML document (each dashboard page is its own self-contained
  template per the existing convention; no shared layout).
- `pkg/dashboard/templates/account.html` (modified) replaces the
  disabled "Connect GitHub (coming in M7.5 slice 8)" stub with a
  working form POST to `/dashboard/install/connect`. The handler
  at `renderAccount` mints the `connect_github` CSRF envelope
  (mirroring the `delete` + `restore` envelope pattern).
- `pkg/dashboard/templates/apps_list.html` (modified) adds a
  `Deploy from the dashboard →` button alongside the existing CLI
  snippet in both empty-state blocks.
- `cmd/apid/spec_compliance_test.go::schemaSpecOnly` adds
  `TemplateView` (mirrors the precedent for `RaiseOverageCapRequest`,
  `ChangePlanRequest`, etc. — inline DTOs in cmd/apid/handlers_*.go).
- The `TemplateView` schema was added to both `api/openapi.yaml`
  and `pkg/apid/openapi.yaml` (must stay byte-identical per
  convention).

### Bind endpoint is unchanged

`bindAppToRepo` (handlers_install_github.go:207, ~118 lines) is
load-bearing §11 logic and is over the 50-line cap. PR-3 does
NOT refactor this handler — the §11 ownership-proof story is
exactly what the §11 proof requires, and refactoring would break
the existing tests. The over-cap is documented as a future
cleanup (Mega-C or follow-up ADR).

## What we did NOT do

- **Reject option (b) — API-key-accepting bind endpoint.** Adding
  a `/v1/apps/{slug}/install/bind` route that accepts the API-key
  auth path would let a CLI-only customer bind without the §11
  cookie proof. Rejected because it lets an API key impersonate a
  GitHub identity it doesn't own.
- **Reject option (c) — CLI-minted session cookie.** A
  `gregale connect repo <owner>/<name> --cookie <sid>` flow would
  reuse the dashboard's bind endpoint from the CLI side. Rejected
  because it hands the cookie out of the browser and the dashboard
  session is anchored to (sid, IP, UA) — a CLI that mints the
  cookie has none of those guarantees.
- **Defer option (d) — API-key-accepting bind in PR-9's
  session-back cut-over.** A future PR-9 may add a per-customer
  API-key-with-github-identity credential that lets the CLI bind
  without a browser hop. The §11 proof becomes "the API key carries
  the github_login claim signed by the host" — same proof shape,
  different transport. PR-9's design is out of scope here; the
  current PR doesn't preempt it.
