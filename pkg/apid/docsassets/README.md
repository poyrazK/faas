# Vendored API reference assets

Served by apid at `/docs/assets/<file>` for the API reference page (`/docs`).
Byte-identical to `swagger-ui-dist@5.17.14` from npm (Apache-2.0); the page
pins the same SRI digests and the package tests recompute them.

| File | Source | SRI |
|---|---|---|
| `swagger-ui-bundle-5.17.14.js` | `https://unpkg.com/swagger-ui-dist@5.17.14/swagger-ui-bundle.js` | `sha384-wmyclcVGX/WhUkdkATwhaK1X1JtiNrr2EoYJ+diV3vj4v6OC5yCeSu+yW13SYJep` |
| `swagger-ui-5.17.14.css` | `https://unpkg.com/swagger-ui-dist@5.17.14/swagger-ui.css` | `sha384-wxLW6kwyHktdDGr6Pv1zgm/VGJh99lfUbzSn6HNHBENZlCN7W602k9VkGdxuFvPn` |

To upgrade, add the new release under new versioned names, then update the
paths in `docsassets.go` and the version and digests in
`pkg/apid/openapi_handler.go`.
