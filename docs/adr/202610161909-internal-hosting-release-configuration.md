# ADR-202610161909: Internal hosting release configuration

## Status

Accepted for the default-off internal pilot.

## Context

Static releases currently describe files and SPA fallback. Real frontend output
also needs redirects, application response policies and an HTML 404 page. Live
Cloudflare qualification is not yet available, but these bounded semantics can
be implemented and verified locally without changing the provider architecture.

## Decision

Add optional version-1 configuration to the immutable `HostingManifest` JSON.
Consume the root `gregale-hosting.json` in CLI/browser output-folder deployment;
exclude it from public files. Validate unknown fields, bounded exact same-site
redirects and cycles, approved lowercase security headers with bounded exact or
terminal-prefix paths, and a present HTML 404 file. No customer code, regex,
external redirects, rewrites, proxies or custom caching directives execute.

Authentication and routing precede every response. Redirects precede file/SPA
lookup; custom HTML 404s follow it without changing 404 to 200/304. Pinned release
URLs skip redirects. Headers follow the release supplying the selected bytes and
match the logical request path. Ordered later rules win. Existing immutable file
cache keys and platform-controlled response fields stay authoritative.

Use independent default-off API runtime, Worker/Terraform, CLI and dashboard
build gates. API finalization rechecks admission after storage work. Configured
releases fail closed when Worker support is off, including warm caches. Safety
reconciliation, revocation and cleanup remain independent of configuration
admission. Existing unconfigured releases need no data migration. Upgrade all
writers/Workers before admitting configured manifests: old Workers ignore the
new JSON field and cannot enforce its policies.

## Consequences

Rollback restores content and response rules atomically through existing release
selection. No per-site daemon/VM, schema migration or new serving-path provider
RPC is introduced. Metadata fits the existing manifest storage reservation;
limits live in `pkg/api/limits.go`. File hashing and browser upload transport keep
their existing boundaries. Rule processing is bounded by small counts and values.

This is not production qualification. Local Go/Worker shared fixtures, API, CLI,
browser and store coverage verify semantics; live Cloudflare compatibility,
regional behavior, provider usage and launch acceptance remain pending.
The [operator guide](../internal-frontend-hosting-config.md) defines exact semantics
and the enable/disable sequence.
