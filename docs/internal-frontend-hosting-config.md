# Internal hosting release configuration

Release redirects, approved response headers and a custom 404 page are an
independent, internal-only opt-in. See
[ADR-202610161909](adr/202610161909-internal-hosting-release-configuration.md).
They require no per-site VM or running process and make no additional provider
API call on the serving path. Rules live in the existing immutable release
manifest; promotion and rollback select both files and configuration.

## Configuration file

Place `gregale-hosting.json` at the **root of the prebuilt output folder**:

```json
{
  "version": 1,
  "redirects": [
    { "from": "/old", "to": "/docs/", "status": 308 }
  ],
  "headers": [
    { "path": "/*", "values": { "content-security-policy": "default-src 'self'", "permissions-policy": "camera=()" } },
    { "path": "/docs/*", "values": { "x-frame-options": "DENY" } }
  ],
  "not_found": "404.html"
}
```

The file is consumed as release metadata and **never uploaded as a public file**.
The root filename is reserved by the API and Worker. The CLI rejects symlinks
and special files, including a symlink used for the configuration. Unknown JSON
fields, unsupported versions and invalid policies fail before release creation
in the CLI/dashboard and again at the API. The API also validates direct clients.
The browser performs validation before obtaining upload grants.

`version` must be `1`. Other fields are optional. Limits come from
`pkg/api/limits.go` and are advertised by capabilities: 32 KiB of configuration,
32 redirects, 16 header rules, 8 values per rule, and 2048 printable ASCII bytes
per header value. The whole release manifest remains limited to 512 KiB.
Metadata uses the existing manifest reservation; content file count and payload
bytes exclude this configuration file. The HTML 404 file counts as normal content.

## Routing and response rules

1. Access authentication and enabled site/domain routing are checked first on
   every request, including warm cache hits, redirects, HEAD and conditionals.
2. Exact redirects are evaluated before files and SPA fallback. Supported status
   codes are 301, 302, 307 and 308. Sources are unique; redirect cycles are rejected.
3. Normal file/directory lookup and retained fingerprinted assets follow existing
   behavior. SPA fallback still requires an extensionless HTML navigation.
4. If unresolved and `Accept` includes `text/html`, the configured HTML 404 file
   from the selected release is served with **HTTP 404**, including HEAD and
   conditional requests. Other missing requests retain the JSON 404 response.

Redirect sources/targets are canonical absolute **same-site paths**. Query
strings, fragments, external URLs, protocol-relative paths, percent escapes,
dot segments and Gregale's reserved paths are rejected in rules. Incoming query
strings are discarded when redirecting; no token or query interpolation is done.
This version has no redirect wildcards, regular expressions, rewrites or proxies.

Header paths are exact request paths or a directory prefix ending in `/*`.
`/docs/*` matches `/docs/intro`, not `/docs` or `/docstring`. Rules are applied in
file order; later matches replace an earlier value for the same header. Matching
uses the logical requested path, before directory index/SPA/404 resolution.
Retained assets use their own release's header rules.

Header names must be lowercase and on this allowlist:

- `content-security-policy`, `content-security-policy-report-only`
- `permissions-policy`, `referrer-policy`, `x-frame-options`
- `cross-origin-opener-policy`, `cross-origin-embedder-policy`,
  `cross-origin-resource-policy`

These values express application policy and must suit the application's scripts,
assets and integrations. They cannot override Gregale's authentication, cookies,
CORS, content type/length, routing diagnostics, ETag or cache controls. Control
characters, non-ASCII and leading/trailing whitespace are rejected. Matched rules
apply to file responses, redirects, custom 404s, HEAD and successful 304 responses;
platform error/denial responses never receive release-configured headers.
Redirects are private/no-store. Other content keeps private revalidation and the
existing immutable, authorization-gated shared body cache.

Pinned `/_gregale/releases/RELEASE_UUID/...` URLs use that release's headers and
404 page but skip redirects to preserve explicit file inspection. These URLs do
not rewrite absolute asset references and are not isolated preview deployments.

## Default-off rollout and disabling

Deploy compatible apid writers, CLI/dashboard and Worker code **before** enabling
any of these settings. Older Workers ignore the new manifest field, so do not
admit configured releases while any serving Worker still uses old code. No SQL
migration or change to existing release JSON is required. Unconfigured releases
retain their current behavior with every new flag off.

- API bootstrap: `FAAS_FRONTEND_HOSTING_CONFIG_ENABLED=1`; corresponding hot
  runtime setting: `frontend_hosting_config_enabled`, default false. It gates
  creation, finalization and promotion of configured releases. Finalization
  rechecks the setting after provider publication before marking a release ready.
- Worker: `FAAS_FRONTEND_HOSTING_CONFIG_ENABLED="true"`; Terraform's independent
  `release_config_enabled` defaults false. Generated apid environment keeps
  admission at `0` even when the Worker setting is enabled.
- CLI: `FAAS_FRONTEND_HOSTING_CONFIG_ENABLED=1`, alongside the existing internal
  hosting CLI opt-in. The output folder is read by `gregale sites deploy`.
- Dashboard build: `NEXT_PUBLIC_FRONTEND_HOSTING_CONFIG_ENABLED=true`. Both this
  build flag and the API's `config_enabled` capability are required to ingest a
  config file. A file present while disabled is an error, never published as content.

Disabling the API setting blocks new configured intent; it does not change
already published edge routing. Reads, suspension, retirement, deletion and
reconciliation remain available under their existing hosting/operator gates.
Disabling the Worker setting fails configured releases closed with HTTP 503,
including already warm caches; it does not silently drop CSP or redirect rules.
It can also block a retained asset whose release has configuration. To continue
serving without configuration, promote a compatible unconfigured release while
Worker support is still enabled, then disable it after propagation. The original
Worker kill switch still disables all hosting traffic.

## Verification

Go and Worker tests share accepted/rejected configuration fixtures. Additional
API/CLI/browser and memory/PostgreSQL store tests cover gates, file exclusion,
immutable metadata and byte/count limits. Worker tests cover precedence,
HEAD/304/404, warm-cache flag revocation, custom-domain Access, retained assets,
promotion and rollback. These local tests do not establish live Cloudflare
DNS/TLS/cache behavior or production readiness; those pilot gates remain pending.
