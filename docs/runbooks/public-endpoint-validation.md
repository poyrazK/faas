# Public endpoint validation

`gatewayd-public` intentionally listens on loopback. Caddy (and, where used,
the CDN) owns the public TLS edge and proxies to `http://127.0.0.1:8080`.
Therefore a local `curl http://127.0.0.1:8080` is not proof that the public
release endpoint works.

Run this from a machine that can reach the public DNS name:

```sh
cd /opt/faas
PUBLIC_ENDPOINT_URL=https://my-api.gregale.dev \
PUBLIC_HTTP_URL=http://my-api.gregale.dev \
make public-endpoint-check
```

Use a real deployed app slug in the current `<slug>.gregale.dev`
shape. The HTTPS curl probe performs normal certificate and hostname
verification, so a missing wildcard SAN or an otherwise uncovered
hostname fails the release check before any HTTP assertions run.

The check verifies all of the following:

- the URL is HTTPS and the certificate/hostname validates through curl;
- the configured probe path (default `/status`) returns 2xx;
- `Strict-Transport-Security` is present with `max-age >= 31536000`;
- a representative standard-library API client user agent (`Python-urllib/3.13`)
  receives the same 2xx response instead of a CDN browser-integrity block;
- optional HTTP traffic redirects to HTTPS.

The Cloudflare zone keeps Browser Integrity Check enabled on the apex and
reserved platform hosts. A Configuration Rule named
`Allow API clients on customer app hosts` disables that setting only for
`*.gregale.dev` hostnames outside the reserved host list. If the API-client
probe fails with Cloudflare error 1010, inspect that rule before changing any
zone-wide security setting.

For a site whose authenticated status page is not public, choose another
unauthenticated 2xx path:

```sh
PUBLIC_ENDPOINT_URL=https://my-api.gregale.dev \
PUBLIC_ENDPOINT_PATH=/ \
make public-endpoint-check
```

On the control-plane host, separately verify the private service and Caddy
upstream:

```sh
systemctl is-active --quiet faas-gatewayd-public
curl --fail --silent http://127.0.0.1:9092/readyz
curl --fail --silent -o /dev/null http://127.0.0.1:8080/
```

A public pass is required before announcing a release. The local checks are
diagnostics only; they do not replace the external HTTPS check.
