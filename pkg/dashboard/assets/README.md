# Vendored dashboard scripts

Served by apid at `/dashboard/assets/<file>` so the dashboard's
Content-Security-Policy allows scripts from `'self'` and its nonce only.
Each file is byte-identical to the npm release below; the templates pin the
same SRI digest and `TestVendoredAssetsMatchTheirIntegrity` recomputes it from
the embedded bytes.

| File | Package | Source | SRI | Licence |
|---|---|---|---|---|
| `htmx-2.0.4.min.js` | `htmx.org@2.0.4` | `https://unpkg.com/htmx.org@2.0.4` (`dist/htmx.min.js`) | `sha384-HGfztofotfshcF7+8n44JQL2oJmowVChPTg48S+jvZoztPfvwD79OC/LTtG6dMp+` | 0BSD |
| `htmx-ext-sse-2.2.2.js` | `htmx-ext-sse@2.2.2` | `https://unpkg.com/htmx-ext-sse@2.2.2` | `sha384-fw+eTlCc7suMV/1w/7fr2/PmwElUIt5i82bi+qTiLXvjRXZ2/FkiTNA/w0MhXnGI` | 0BSD |

To upgrade, download the new release, compute its digest
(`openssl dgst -sha384 -binary <file> | openssl base64 -A`), add it under a new
versioned name, and update the paths in `assets.go`, the templates' `src` and
`integrity` attributes, and the test's pinned set.
