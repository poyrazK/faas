# Internal hosting infrastructure

This root configuration provisions one dedicated internal pilot: private R2
storage, staging expiration, a routing KV namespace, an email-restricted
Cloudflare Access application, and the versioned hosting Worker. Both the
Worker switch and wildcard DNS/routing default to **off**. It does not change
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
returns `frontend_hosting_disabled`, including for previously cached files.
Disabling Gregale's API flag separately rejects new operations and lets existing
edge leases expire. Detaching the route alone is not a feature-switch test.

Retire/delete all sites and verify storage cleanup before tearing down a pilot.
There is no automatic destroy command. Removing bucket protection requires an
explicit infrastructure edit after the cleanup catalog and artifacts are empty.

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
