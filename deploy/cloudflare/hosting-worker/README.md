# Internal frontend hosting Worker

This Worker reads only published static releases from a private R2 bucket.
It is default-off, requires verified Cloudflare Access identities and an
explicit internal email allowlist, and checks routing before looking up cached
content. Finite KV leases remain the default; a separate default-off switch
allows durable R2 routing heads for newly opted-in sites.

Configuration, rollout, disable, recovery, and qualification instructions are
in [the internal hosting runbook](../../../docs/internal-frontend-hosting.md).
Use the [Terraform configuration](terraform/README.md) for repeatable pilot
provisioning. Its initial apply leaves serving and wildcard routing off.
`wrangler.toml.example` remains an alternative manual setup; use only one owner
for a Worker's configuration. No production resource or route is enabled by
this directory's presence.

```bash
node --test --test-isolation=none deploy/cloudflare/hosting-worker/worker.test.mjs
python3 scripts/ci/test_internal_frontend_hosting_qualification.py
```

Successful file GETs report `X-Gregale-Cache: HIT` or `MISS`. This describes the
Worker's file cache, after identity/routing checks, and is used by live
qualification. HEAD, conditional 304, and error responses do not claim a cache
hit. `X-Gregale-Release` identifies the immutable release serving a response.
Durable responses also report `X-Gregale-Route-Generation` on GET, HEAD, and
conditional revalidation. The value is evaluated after authorization/routing,
including when file bytes come from cache, and supports
[outage and regional qualification](../../../docs/internal-frontend-hosting-qualification.md).

The local CLI preview uses [shared serving cases](local-preview-cases.json), also executed by `worker.test.mjs` against this authenticated Worker. The local command models one build and does not emulate Access, DNS, TLS, retained releases or CDN propagation. See the [local preview guide](../../../docs/internal-frontend-hosting-local-preview.md).
