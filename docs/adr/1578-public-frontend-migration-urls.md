# ADR-1578: Configure public frontend URLs before migration builds

## Status

Accepted for local implementation.

## Context

Runtime service connections cannot configure frontend bundles. Vite and Next.js
read public URL variables while compiling; sealing those variables as runtime
secrets leaves successful builds pointing at their previous provider.

## Decision

Migration creation accepts `public_service_connections` and `public_build_urls`.
The first maps frontend URL keys to selected declared HTTP/GraphQL dependencies;
the server resolves each target's reviewed `PlanWorkload.public_url` using the
allocated app name and the installation's app domain. The review token includes
the app domain, so creation refuses a changed domain before saving the operation.
The second explicitly supplies an existing public HTTPS base URL, including an
optional base path. Each caller has at most ten combined URL values, each at most
2048 bytes, within a 24,000-byte encoded map limit. Keys use conventional frontend
prefixes and end in `_URL`.

These are deliberately public configuration values. Credentials, query strings,
fragments, IP literals, localhost and private service names are rejected. No
runtime secret is read or copied into a build. A key cannot have both public
configuration and a private runtime value or connection. The console requires
review of the resolved URL and an acknowledgement that it can appear in a browser
bundle. Pasted values for explicitly mapped keys are excluded from secret
submission. Untouched variables retain their existing runtime behavior.

The migration journal retains the public mappings and resolved public URLs.
Unlike secrets, these public values can be displayed after reload. Resume cannot
replace them and restores saved choices only for unfinished callers, preserving
accepted sibling deployments. They are stored in `AppManifest.public_build_urls`
before any build is queued; project reconciliation retains this app intent.
Existing runtime secrets or conflicting runtime values with the same key block
configuration with an actionable error; the migration does not silently delete
customer runtime configuration or leave it shadowing the new image value.

A deployment INSERT trigger atomically captures production source build values
in `deployment_public_build_urls`. An immutable snapshot per deployment avoids
reading mutable app settings when a queued build starts. Legacy rows have empty
configuration; previews and other scopes do not inherit production endpoints.
Ordinary source retries copy the predecessor's snapshot in their creation
transaction. Newly requested production builds capture the current app intent.
All new state queries use sqlc; the state store exposes a narrow optional seam.

Builderd reads only that deployment snapshot and includes the full public map in
the artifact cache recipe. It passes the values in `BuildManifest`. Guest-init
overlays literal variables into every generated Railpack step and the deployment
environment after prepare, before dependency cache decisions and the BuildKit
solve. Dockerfile builds receive sorted build arguments; customer Dockerfiles
must declare the corresponding `ARG` and use it in the compile stage. URL values
never become shell syntax. Warm builder reuse prepares a fresh plan each time.

Successful guests acknowledge the deterministic public configuration digest in
`BuildDone`; builderd refuses an artifact with a missing or mismatched digest.
This fences older guest images that would otherwise ignore the additive field.
Both artifact and dependency layer caching vary when compile inputs change.

## Coverage and limits

This configures public URL inputs; it does not rewrite frontend code, migrate
data, infer API paths, or change CORS or authentication. Public service mappings
use stable app URLs and do not pin public traffic to one deployment. Startup and
cold-wake checks retain their existing server-side coverage; customers must also
exercise the browser flow and allow the frontend origin in their application.
Updating public URL choices requires a separate reviewed migration and rebuild.

Tests cover URL validation, request serialization, saved choices, partial retry,
PostgreSQL snapshot persistence, immutable queued inputs, cache invalidation,
guest plan injection and old guest refusal. Native KVM acceptance must cover
Vite/Next.js and Dockerfile builds, changed URLs on identical source, a warm
second build, and missing guest acknowledgement before shipping.
