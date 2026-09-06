# ADR-012 · `githubd` daemon for GitHub App integration

- **Status:** accepted
- **Superseded (in part, PR-E):** prose referred to the monolithic
  `cmd/gatewayd/` daemon split by ADR-070 into `gatewayd-public` (TLS-only
  edge) and `gatewayd-internal` (routing + wake + proxy). Body is preserved
  verbatim; readers should substitute "gatewayd-internal" for the
  routing/wake/proxy path and "gatewayd-public" for the certmagic/TLS path.
  `cmd/gatewayd/<file>.go` citations in this body are stale; see PR-E for
  the new file locations.
- **Date:** 2026-07-17
- **Decision:** Introduce `cmd/githubd/` as a new daemon that owns the GitHub App surface for push-to-deploy (resolves gap G8). githubd owns: (1) push-webhook receiver for `push` events on the production branch, including release tag pushes resolved against the repository's default production branch, (2) Checks-API status writer (`queued` → `building` → `live`/`failed` with logs link), (3) OAuth callback handler for the install flow, (4) per-repo install-token cache. githubd talks to `apid` over gRPC on `/run/faas/githubd.sock` (mode 0660 group `faas`, ADR-015 unix-socket auth) using the `pkg/githubdgrpc` package (ADR-018 schedd pattern). githubd's own HTTP listener is loopback-only at `127.0.0.1:8083`; the public webhook surface lives on `gatewayd` at `/webhooks/github`, HMAC-verified at the edge, then reverse-proxied to githubd.
- **Why:** outbound `api.github.com` traffic + Checks-token refresh + webhook signature verification don't fit `apid`'s "customer-intent CRUD" ownership. Mixing them in apid would dilute that ownership and pull api.github.com's latency into apid's hot path. The webhook + Checks flows are background-ish (seconds-to-tens-of-seconds); colocating them in their own daemon with its own goroutine budget is the load-bearing shape (CLAUDE.md: "Components talk via Postgres rows + `pg_notify`, or gRPC on unix sockets"). Least-privilege scope review per §11 (spec §11 line 398): the GitHub App requests `Contents:read` + `Checks:write` + `Issues:write` + push webhook — no org-wide access. `Issues:write` is limited to the idempotent PR preview status comment.
- **Consequences:**
  - New daemon `cmd/githubd/` added to the fleet (9th daemon). Follows the `wire.StubRun("M7.5")` bootstrap (mirroring how meterd was bootstrapped in M7) then gets replaced with the real main loop once the gRPC surface is exercised in tests.
  - New `pkg/githubdgrpc/` package with the proto contract (`GetInstallState`, `ExchangeOAuthCode`, `ListInstallableRepos`, `BindAppRepo`, `UnbindAppRepo`, `GetAppBinding`, `CreateDeploymentFromPush`, `WriteCheck`). Generated `*.pb.go` is committed (ADR-013). `make proto` + `make proto-check` are extended.
  - `apid` gains a gRPC client wrapper (`cmd/apid/githubd_client.go`) that converts gRPC errors to `*api.Problem` (mirroring `scheddgrpc.Problem`).
  - `gatewayd` gains a path-routing segment: `POST /webhooks/github` → `VerifyPushSignature` (HMAC-SHA256 over `X-Hub-Signature-256`, constant-time compare, no replay window by default) → `httputil.ReverseProxy` to `http://127.0.0.1:8083/webhooks/github`. Webhook secret lives in `/etc/faas/secrets/` (mode 0400, spec §11 line 398) — gatewayd is the only daemon that reads it.
  - `pkg/githubd/webhook.go` reuses the HMAC primitives from `pkg/stripex/webhook.go:90-104` (`hmac.Equal` + `hmac.New(sha256.New, …)`); only the header parse differs.
  - PR-preview environments are now supported through `pull_request` webhooks. Fork PRs remain refused, and the App uses `Issues:write` for the single idempotent preview status comment rather than `Pull requests:write`.
  - `cmd/builderd/main.go` gains a `LISTEN build_queued` (or whichever notify channel the slice-7 commit picks) so webhook-induced deploys are picked up identically to CLI-induced ones.

## §9 Release-tag promotion policy

Production tag pushes are accepted only when the tag is a valid SemVer with
the conventional `v` prefix (for example `v1.2.3` or `v1.2.3-rc.1`), GitHub
marks the ref as `created`, and the `before` SHA is the all-zero creation
sentinel. A tag update or force-push is acknowledged but ignored with the
reason `release_tag_moved`;
an invalid tag is acknowledged with `invalid_release_tag`. Both decisions
happen before binding lookup, source fetch, or reconcile. This makes a release
tag a one-way promotion boundary without adding customer state or a database
migration; a new version must use a new tag.

- **Rejected alternatives:**
  - **githubd as a module inside apid.** Rejected: apid's responsibility is customer-intent CRUD; outbound api.github.com traffic + Checks token refresh would dilute that, and any apid hot-path regression would now have a third-party-API dependency.
  - **githubd terminates the webhook itself (own public listener).** Rejected: violates the §11 single-public-listener invariant. githubd is loopback-only; gatewayd already has CertMagic + the canonical request-ID injection + rate-limit observability — splitting the public surface doubles the §11 attack surface.
  - **Plain HTTP JSON between apid and githubd.** Rejected: gRPC over the unix socket is the spec-mandated inter-component transport (spec §4 line 94), and gRPC gives us typed errors + a real schema for the RPC surface — hand-rolled JSON would be a regression.
  - **Reuse `pkg/stripex` for the GitHub webhook verifier.** Rejected: stripex is Stripe-shaped (`Stripe-Signature` header, `t=…,v1=…` envelope, 5-min tolerance). The GitHub header (`X-Hub-Signature-256: sha256=…`) has no timestamp/replay window. Lifting `hmac.Equal` + `hmac.New` primitives is fine; the parsing differs.

## Re-evaluation triggers

- **M8 hardening (§11 checklist):** the webhook is HMAC-verified at the gateway edge. If §11 fuzzing finds a verifier bypass, the fix is in `pkg/githubd/webhook.go` + the gatewayd path-routing segment — not a daemon split.
- **Gate-A multi-host (spec §16):** githubd's outbound traffic to `api.github.com` may need to go through a different egress allowlist on the standby host. The transport (gRPC over `/run/faas/githubd.sock`) stays; the egress policy moves to `deploy/nftables/`.
- **A second Git-like provider (GitLab, Gitea):** if the founder expands the ICP, the right shape is `git-deployd` (or rename `githubd` → `gitd`) with provider-specific webhook verifiers behind a shared `pkg/gitdeploy` core. Don't fork githubd per provider.
## §7 Per-tenant webhook secret (PR-D amendment, 2026-08-11)

- **Decision:** the platform-wide `FAAS_GITHUB_WEBHOOK_SECRET` (used by
  `pkg/githubd/webhook.go::VerifyPushSignature`) is supplemented by a
  row-per-installation lookup at `github_webhook_secrets` (migration
  00208). The resolver at `pkg/githubd/webhook_secret.go` reads the
  per-tenant row first; a missing row falls back to the platform
  secret. Fail-closed posture: a DB error on resolve emits the
  Prometheus counter `githubd_webhook_secret_total{status="db_error"}`
  and the webhook is rejected (the gatewayd-internal proxy remains the
  load-bearing edge verifier; the daemon-side re-verify is a defense in
  depth check).
- **Why:** a leaked tenant secret had to be rotated by every
  GitHub App install simultaneously (the previous shape). The
  per-tenant row lets a single tenant rotate without coordinating
  other installs. The cache (60s TTL + singleflight) keeps the
  hot-path latency within §13 budget; the Invalidate() call on the
  admin `set` path keeps the rotation window short.
- **Operational surface:** `gregale github-webhook-secret set
  --installation-id <id> --secret <hex> [--from-stdin]` (admin-scoped
  API key + email allowlist). See
  `docs/runbooks/GithubWebhookSecretRotation.md` for the on-call
  flow.
- **Audit:** every set emits the `githubd.webhook_secret_set` event
  with the operator's account id + the `installation_id`. The
  Prometheus counter `githubd_webhook_secret_total{status="set"}` is
  emitted server-side so a dashboard alert can flag unexpected
  rotation frequency.
- **Rejected alternatives:**
  - **Per-tenant secret stored on `github_installations` directly.**
    Rejected: that row already carries the OAuth handshake state and
    the `account_id` FK; adding the secret there would couple the
    webhook-secret lifecycle (permanent) to the install lifecycle
    (revocable on uninstall). A separate table keeps the two
    concerns decoupled.
  - **Re-key on every webhook (rolling secret).** Rejected: GitHub
    doesn't support per-webhook secrets — the secret is the App
    setting, not the event. The rotation posture is operator-driven,
    not rolling.

## §8 App-level webhook secret correction (2026-09-05)

GitHub App webhook deliveries do not carry a custom installation-secret
selector and GitHub configures one webhook secret for the App. Section 7's
per-installation design therefore cannot authenticate normal GitHub traffic.
The load-bearing path now uses the same `FAAS_GITHUB_WEBHOOK_SECRET` in
`gatewayd-internal` and `githubd`; the installation-scoped resolver and admin
command remain compatibility-only for non-GitHub senders that supply an
explicit installation header. See the rotation runbook for the coordinated
App-secret procedure.
