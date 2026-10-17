# Hide disabled internal frontend hosting from customer discovery

Date: 2026-10-17

Status: accepted for internal implementation; live qualification remains deferred.

The main default-off hosting flag must hide discovery as well as reject writes.
Disabled API routes return the ordinary missing-route 404 before authentication,
CORS, method matching, redirects and idempotency replay. Match the entire decoded,
cleaned `/v1/internal/hosting` namespace, including anonymous OIDC exchange and
unknown children. Reevaluate runtime admission on every request. Preserve existing
inner admission guards, operator restrictions, ownership, scopes and MFA.

With the workstation flag off, hide `sites` from advanced help, explicit help,
manual pages and typo suggestions, and treat direct invocation as an unknown
command. Keep its manifest for contract tests. Local opt-in does not grant remote
operator access. Customer completion and generated Markdown remain customer-only.

The dashboard build flag controls the bundler dependency graph: resolve its client
entry to a server-only missing-page stub while off, for both Turbopack and webpack.
Keep route and navigation guards. CI builds with the flag off and scans emitted
public JavaScript and source maps for hosting implementation markers. This is
regression coverage for known entry points, not a secrecy guarantee for repository
source or arbitrary future imports. Rebuild when changing public build flags;
retire previously published enabled assets separately.

Worker serving remains independently controlled and default-off. Disabled serving
returns a generic non-cacheable 404. Enabled serving still requires a verified
Cloudflare Access identity with an explicitly internal email, including cache
hits. API disable does not stop durable serving, cleanup or previously accepted
reconciliation work. Do not couple those switches or alter durable availability.

Release with API runtime admission disabled, a fresh dashboard build with its
public flag off, and Worker serving/routing disabled or confined to an isolated
Access-protected internal pilot. Bootstrap environment values do not override a
persisted runtime setting. Deployed settings and provider behavior require live
operator evidence, which remains deferred. Source/docs in public repositories
remain discoverable independently of product flags.

The public OpenAPI and generated SDK inventories explicitly exclude the internal
hosting routes. Route parity must not be satisfied by advertising pilot endpoints.
Register hosting tables in environment-clone schema inventory with their existing
configuration/data, account-identity and operational boundaries. Known schema is
not a qualified hosting-copy strategy: preserve the configuration and artifact
strategy blockers, and do not clone credentials, domain claims or serving receipts.
Public clone-admission errors hide hosting schema blockers unless hosting is
enabled and the caller is an operator; clone admission remains closed throughout.

The pilot migrations must tolerate replay of their existing DDL after missing
ledger receipts. Replay preserves accepted promotion times, private staged
releases, reserved artifact bytes, durable publication generations and pending
age, retry state and terminal CI cancellation. Guard legacy backfills and routing
refreshes so they execute only during the original schema transition.
