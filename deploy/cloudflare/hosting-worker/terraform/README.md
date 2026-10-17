# Internal hosting infrastructure

This root configuration provisions one dedicated internal pilot: private R2
storage, staging expiration, a routing KV namespace, an email-restricted
Cloudflare Access application, and the versioned hosting Worker. Both the
Worker switch, durable-routing switch, and wildcard DNS/routing default to **off**. It does not change
Gregale's API flag, provision R2 credentials, create an identity provider, or
deploy Gregale daemons/migrations.

Use Terraform 1.7 or newer; CI uses checksum-pinned 1.12.2. The Cloudflare
provider is pinned and its signed package hashes are committed in
`.terraform.lock.hcl`. Run the following from the repository root. Keep the
checkout and Terraform state on an operator-owned machine; use an encrypted,
locked remote backend for a shared pilot. Local state/tfvars/plans are ignored.

## Prepare a dedicated pilot

1. Choose a separate registrable domain and an active Cloudflare zone on the
   pilot's account. The configuration checks the actual zone apex and account.
   `platform_domains` must list the registrable domains used by the existing
   dashboard, API, and apps; apid separately checks public-suffix isolation.
   Universal SSL must cover the wildcard before qualification.
2. Use an existing Cloudflare Zero Trust organization and identity provider.
   Set a nonempty explicit internal email list. The Access application and
   Worker independently enforce this list; there is no bypass/service-token
   policy.
3. Set `CLOUDFLARE_API_TOKEN` in the operator environment. Provisioning needs
   account permissions for Workers Scripts, Workers KV Storage, Workers R2
   Storage, and Access Apps and Policies, plus DNS, Workers Routes, and zone
   read permissions on the dedicated zone. Keep this token out of tfvars and
   Gregale's runtime. Apid needs separate, narrower R2 publication credentials
   and a routing-KV token as described in the
   [hosting runbook](../../../../docs/internal-frontend-hosting.md).

```bash
umask 077
cp deploy/cloudflare/hosting-worker/terraform/terraform.tfvars.example \
  deploy/cloudflare/hosting-worker/terraform/terraform.tfvars
# Edit the local tfvars with the real isolated account/zone/identities.
terraform -chdir=deploy/cloudflare/hosting-worker/terraform init -input=false
terraform -chdir=deploy/cloudflare/hosting-worker/terraform plan -out=pilot.tfplan
terraform -chdir=deploy/cloudflare/hosting-worker/terraform apply pilot.tfplan
terraform -chdir=deploy/cloudflare/hosting-worker/terraform output -json apid_environment
```

Initial apply creates no DNS record or Worker route. R2's `r2.dev` endpoint,
`workers.dev`, and Worker preview URLs are explicitly disabled. No public R2
custom domain is created. Only `hosting/staging/` expires after one day;
published objects are retained for Gregale's retirement workflow. The bucket
has `prevent_destroy` to protect artifacts and the cleanup catalog.

Install the output's non-secret apid settings together with scoped credentials
through the existing sealed-environment deployment workflow. Deploy the
hosting migration/daemons and leave `frontend_hosting_enabled` false. Do not
source Terraform JSON as a shell file. The origin fingerprint is derived from
the exact endpoint and bucket and agrees with apid's placement identity.

## Attach and qualify

After reviewing Access, zone isolation, wildcard TLS, and Gregale's deployment,
set `attach_route = true` in the local tfvars, then review/apply a fresh plan.
The proxied wildcard DNS record uses the reserved non-serving `192.0.2.1`
origin; static traffic has no path to a Gregale VM. The Worker remains disabled.

For the trial, explicitly set `worker_enabled = true`, review/apply again, and
enable Gregale's internal runtime switch through the operator console. Both
gates are necessary. Use this dedicated pilot for the
[live qualification runner](../../../../docs/internal-frontend-hosting.md#live-qualification).
Do not also deploy this Worker with Wrangler: Terraform owns its versions,
bindings, deployment, and route.

Disabling `worker_enabled` and applying leaves the protected route attached and
returns a generic `not_found` 404, including for previously cached files.
Disabling Gregale's API flag separately rejects new operations and lets existing
edge leases expire; durable sites retain their last published state. Detaching
the route alone is not a feature-switch test.

To qualify durable routing, upgrade every apid writer/controller, then explicitly
set `durable_routing_enabled = true` and review/apply the Worker change. Enable
the separate API creation flag and create a fresh site as described in the
[durable-routing rollout](../../../../docs/internal-frontend-hosting.md#durable-routing-separate-internal-opt-in).
Existing sites keep their mode. Disabling the API creation flag affects future
sites; it does not stop traffic to existing durable sites. Do not revert to an
older apid binary while durable sites exist.

Retire/delete all sites and verify storage cleanup before tearing down a pilot.
There is no automatic destroy command. Removing bucket protection requires an
explicit infrastructure edit after the cleanup catalog and artifacts are empty.
Durable routing leaves small terminal heads in `hosting/routes/` after site
cleanup. Preserve them while the pilot can receive traffic or delayed writes;
do not add a lifecycle expiration for this prefix.

## Browser uploads (independent opt-in)

The internal Operations hosting dashboard needs an explicit browser CORS
allowlist on this private bucket. Set `dashboard_upload_origins` to the exact
HTTPS origin of the dedicated operations console, review a plan, and apply it.
The default empty list installs an empty ruleset. Removing all origins clears
the ruleset again, including after a previously enabled pilot.

Only `PUT` and `content-type` are allowed; there is no browser read/list/delete
rule, wildcard origin, or public bucket endpoint. CORS applies to the bucket
and is not an authorization boundary: the API's five-minute signed URLs
authorize only individual `hosting/staging/SITE/RELEASE/PATH` objects. Browser
uploads omit cookies, authorization headers, and referrers. Browsers provide
the signed Host and Content-Length from the URL and File body. Finalization
still verifies every size and SHA-256 before immutable publication.

The operations origin must also be trusted by apid's existing session-write
origin guard (normally `https://operations.<apps-domain>`). A random preview
hostname in CORS alone does not grant API access. Use the same dedicated pilot
API for the operations deployment; do not point a development preview at
production. This configuration neither enables the dashboard build flag nor
changes any API/Worker feature switch.

## Custom hostnames (independent opt-in)

`configure_custom_domains` and `custom_domains_enabled` both default false. Enable Cloudflare for SaaS on this dedicated zone before opting into infrastructure with `configure_custom_domains`. With attached durable
routing, this changes the zone's Worker route to `*/*`, adds the reserved
non-serving `customers.<hosting-domain>` DNS target, and configures it as the
SaaS fallback. `custom_domains_enabled` separately controls Worker admission without removing
that infrastructure. Main serving and API admission remain independently controlled;
the API output leaves the domain management flag at `0`.

The reusable internal Access policy stays Terraform-owned. Apid manages only
durably bound per-domain hostnames and Access applications with a separately injected
domain token. Follow the [domain runbook](../../../../docs/internal-frontend-hosting-domains.md)
for external DNS, credentials, operator-assisted recovery, rollout, and live evidence.
Drain/qualify every custom domain before removing the SaaS infrastructure opt-in.
Never expire `hosting/domains/` terminal heads or discard retained hostname
reservations while reusing the pilot bucket.

## Portable validation

These checks do not authenticate to Cloudflare or create cloud resources:

```bash
terraform -chdir=deploy/cloudflare/hosting-worker/terraform fmt -check -recursive
terraform -chdir=deploy/cloudflare/hosting-worker/terraform init -backend=false -input=false -lockfile=readonly
terraform -chdir=deploy/cloudflare/hosting-worker/terraform validate
terraform -chdir=deploy/cloudflare/hosting-worker/terraform test
```

The mocked tests cover default-off routing/serving, private alternate
endpoints, staging-only expiration, origin fingerprints, and rejection of
empty identities, platform domains, wrong zones, and cross-account placement.
They validate configuration and provider schema; real account permissions,
Access/TLS propagation, and serving behavior need live qualification.


## Traffic observations (independent opt-in)

`configure_analytics = false` and `analytics_enabled = false` are the defaults.
The first attaches a dedicated Analytics Engine dataset binding; the provider
creates the dataset on its first write. The second enables nonblocking,
privacy-limited Worker observations without altering serving. API output leaves
analytics management disabled and never includes its separate read token.
Review provider pricing/retention before opting in. See the
[traffic runbook](../../../../docs/internal-frontend-hosting-traffic.md) for scoped
credentials, API/dashboard flags, sampling semantics and live qualification.
Disabling collection does not delete observations or stop static serving.

## Release configuration (independent opt-in)

`release_config_enabled` defaults false. It supplies the Worker's independent
`FAAS_FRONTEND_HOSTING_CONFIG_ENABLED` serving switch; generated apid environment
keeps admission disabled. Upgrade all apid writers/Workers before admitting
configured manifests. See [the release configuration guide](../../../../docs/internal-frontend-hosting-config.md)
for file format, limits, API/CLI/dashboard opt-ins and fail-closed disabling.

## Private previews (independent opt-in)

`previews_enabled` defaults false. It adds no per-preview provider resources and
sets only the independent Worker serving switch; generated apid environment
keeps admission off. Previews use the existing wildcard DNS, TLS, Worker route
and internal Access application and require durable routing. Follow the
[preview guide](../../../../docs/internal-frontend-hosting-previews.md) for the
coordinated migration/upgrade, separate API/dashboard/CLI gates and safe cleanup.
