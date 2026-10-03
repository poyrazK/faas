# ADR-411: Bounded production object transfers

Status: Accepted (2026-10-03)

## Context

The registry separates total object, single PUT and multipart part limits, but
the deployed gateway fixes its stream deadline at 30 minutes and cannot configure
upload concurrency or spool reservations. The beta deployment puts the branded
endpoint behind a CDN and caps requests at 64 MiB. Raising only the object limit
does not establish a usable production transfer path.

## Decision

Add an operator-owned `transfer` policy to the provider registry. Explicit
`proxied` profiles enforce the 64 MiB service request/part ceiling; `direct`
profiles allow the existing 5 GiB request/part and 5 TiB multipart ceilings.
An omitted policy preserves existing registries' byte limits and selects direct
semantics; deployed examples declare proxied explicitly. Direct deployment uses
the existing branded hostname with DNS-only routing to Caddy's TLS origin, with
no redirects or URL rewriting. The Ansible edge mode and registry profile must
agree. This does not change DNS or enable ingress automatically.

Configure transfer time in whole seconds, from one second to 24 hours. Apply one
request context deadline across authentication, body validation/staging and
forwarding. The gateway socket read deadline interrupts stalled bodies; a small
existing settlement allowance permits the final response and durable settlement.
The daemon's server uses the same bound, including through metrics/tracing
wrappers. Provider streaming reads and writes respect the caller's remaining deadline;
calls without one retain the previous 30-minute bound. Native GCS reader/writer
exchanges use a copied HTTP client with that stream bound, so the shared OAuth
client's 20-second metadata timeout cannot truncate them or race with concurrent
requests. Control RPCs retain their existing shorter limits. A timeout after dispatch remains uncertain and never
proves that a write failed or permits replay/refund.

Expose configured upload concurrency (1–64), aggregate spool bytes (at least one
single PUT, at most 5 GiB), and retained filesystem free space (1 byte–5 GiB).
Defaults remain four uploads and a 1 GiB free-space reserve. Reservations remain
atomic, release on every terminal request path, and cover complete single-PUT
staging before provider dispatch. Multipart parts stream through the same upload
slots and existing durable part admission; they do not buffer entire parts.

Advertise single PUT, part, total object limits, profile and transfer deadline
through the bucket catalog and typed Go/Node/Python clients. Match Caddy's origin
transport deadlines to the registry and keep replay disabled. Preserve signed
Host, key/query bytes and Content-Length. The gateway enforces decoded body
limits, including AWS chunked framing; the proxy does not impose an incorrect
decoded-size limit on encoded frames.

## Verification

Qualify configuration boundaries, a streamed upload larger than the beta request
ceiling through the local AWS SDK/gateway/S3 adapter, a 65 MiB multipart part
through completion/list/read with no whole-part staging, socket deadline cleanup,
one combined staging/forwarding budget, and conservative dispatched receipts
after timeout. Use memory and PostgreSQL state plus local HTTP fixtures. No real
provider environment is required by the user's task. Memory/PostgreSQL tests
verify exact retained bytes and accounting. Local S3 and Google Storage SDK fixtures
qualify native read/write versus metadata deadlines without ADC or provider
access. Local Caddy fixtures render both
profiles, stream 65 MiB at the proxy layer, and check signed request fields;
the proxied service still enforces its decoded 64 MiB ceiling. Ansible syntax,
API/SDK discovery, related regressions, focused races and repository checks
qualify this implementation increment.

References: [Go response deadlines](https://pkg.go.dev/net/http#ResponseController),
[Caddy transport timeouts](https://caddyserver.com/docs/caddyfile/directives/reverse_proxy),
[Cloudflare upload limits](https://developers.cloudflare.com/network/maximum-upload-size/).
