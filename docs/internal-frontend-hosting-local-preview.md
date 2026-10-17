# Local frontend serving preview

`gregale sites serve` runs a validated snapshot of prebuilt static output using
Gregale's current-build content rules. It is internal and default-off. It needs
no account login, API connection, Cloudflare credentials, Node runtime, or remote
resource. It never builds or executes source code on the server.

```sh
export FAAS_FRONTEND_HOSTING_ENABLED=1
export FAAS_FRONTEND_HOSTING_LOCAL_PREVIEW_ENABLED=1
gregale sites serve --path dist
# Use the same SPA setting you would deploy:
gregale sites serve --path dist --spa --port 0
```

The default port is 4173. Port `0` asks the OS for an available port. The CLI prints
the exact `http://127.0.0.1:PORT` URL after validation. Open that URL directly.
There is no public host/bind option, CORS opt-in or backend proxy. Browser requests
from another site, foreign Origin/Host headers and non-loopback peers are rejected.
Ctrl-C or SIGTERM stops the server with bounded HTTP shutdown. No flag or state
on Gregale's API or Worker changes when starting or stopping a local preview.

Validation uses the existing deployment scanner: canonical paths, regular files,
no symlinks/special files, index.html, SHA-256 digests, file/count/total limits and
release configuration validation. Before listening, the CLI reads through a bound
filesystem root and verifies snapshot bytes against that manifest. A file changing
while captured fails validation. Configuration metadata is never served as a file.

The server keeps an immutable copy of one build. Edit or rebuild, then restart
the preview to capture the new build. There is no watcher or hot reload. Payload
memory is bounded by the existing 100 MiB release limit; startup additionally
allocates manifest metadata and temporary read/copy buffers. Gregale servers and
provider resources incur no cost from this local command.

For redirects, approved response headers or custom HTML 404s, include the existing
`gregale-hosting.json` file and enable the independent config opt-in:

```sh
export FAAS_FRONTEND_HOSTING_CONFIG_ENABLED=1
gregale sites serve --path dist
```

Serving follows the Worker contract for exact files, directory indexes, redirect
precedence and query stripping, logical request-path header matching, HTML-only
extensionless SPA navigation, custom 404 status, content types, HEAD and exact
strong ETag matches. Range requests and weak/list ETags retain the current Worker
behavior: full content rather than partial or conditional success. No directory
listing or general-purpose filesystem serving is available.

Responses include `X-Gregale-Local-Preview: true`. Production release IDs, route
and cache receipt headers are not fabricated. This model covers the selected
build only: retained older releases, pinned release URLs, Access authentication,
DNS, TLS and shared CDN caching/propagation need separate qualification. Browser
HTTP transport details such as automatic Date/entity headers are runtime-owned.
Local success is feedback about the build, not a production readiness result.

`deploy/cloudflare/hosting-worker/local-preview-cases.json` is executed by the Go
local handler tests and the actual authenticated Worker tests. It locks down
request/status/body/selected-header behavior, including SPA and custom 404 edge
cases. CLI tests cover flags, invalid output, config exclusion, symlink rejection,
snapshot stability, loopback binding, Host rejection, output failure and shutdown.
