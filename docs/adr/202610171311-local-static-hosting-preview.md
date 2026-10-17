# Local static hosting preview

Date: 2026-10-17

Status: accepted for internal implementation; live qualification remains deferred.

Add default-off `gregale sites serve --path dist` for the existing static output
contract. The main CLI hosting flag and independent
`FAAS_FRONTEND_HOSTING_LOCAL_PREVIEW_ENABLED` must be enabled. Configured builds
also require the existing release-configuration CLI flag. This command needs no
API credentials, creates no remote resources and executes no build or source code.

Validate using the existing deployment manifest scanner, including config/file
limits, then bind file reads to an os.Root, verify the actual snapshot bytes against
the manifest digests and copy them into an immutable in-memory snapshot before
opening a listener. Open the root before scanning so rename races cannot switch
the root used for snapshot reads. Read only declared regular files within the
root and bound each read by its declared size plus one. The payload is bounded by
the existing release limit; startup may temporarily hold extra read/copy buffers.
Files, configuration and MIME metadata cannot change under a running preview.

Listen only on IPv4 127.0.0.1, accepting the selected authority, loopback peers,
and same-origin requests. Reject foreign Host/Origin and cross-site browser fetch
metadata. There is no public bind option, proxy, CORS opt-in, credential lookup,
provider request, directory listing, watcher or hot reload. Ctrl-C/SIGTERM shuts
down with bounded HTTP timeouts. The CLI prints the exact URL after validation.

Implement one-build content semantics in Go: exact files and directory indexes,
redirect precedence, logical request-path header rules, extensionless HTML-only
SPA fallback, custom HTML 404s, content types, HEAD and exact strong ETag matching.
Test the same fixtures against the actual authenticated Cloudflare Worker and
local handler to detect drift. Do not modify the production Worker serving or
Access path. Provider trace/cache metadata, retained releases and pinned URLs,
Access, DNS, TLS and CDN propagation are outside the local model. A local result
is developer feedback, never evidence of live serving health or production readiness.
