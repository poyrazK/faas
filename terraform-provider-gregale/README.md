# Gregale Terraform provider

This directory contains the first provider contract for Gregale. It is a
separate Go module so the provider can evolve and be released independently of
the control plane.

The initial surface is intentionally small:

- `gregale_app` manages an API app through the public app lifecycle API.
- `gregale_domain` manages a custom hostname binding and exposes DNS/TLS state.
- `gregale_alert` manages an app alert rule and exposes evaluation state.
- `gregale_cron` manages a scheduled app invocation and exposes scheduler state.
- `gregale_env` manages scoped app environment variables without storing values in state.
- `gregale_secret` manages scoped app secrets without storing plaintext in state.
- `gregale_deployment` deploys a digest-pinned OCI image or GitHub source ref and exposes lifecycle and preview metadata.
- `gregale_tcp_listener` manages a stable public raw TCP listener for an app.
- `gregale_static_egress_ip` pins a stable public IPv4 address for an app's outbound traffic.
- `gregale_private_network` manages a Gregale-owned provider-neutral private network, including reusable CIDR and protocol/port firewall policy.
- `gregale_private_network_attachment` manages an app's provider-neutral private-network attachment and reconciliation state.
- `gregale_private_network_peering` manages a provider-neutral peering between two Gregale private networks.
- `gregale_project_environment_config` manages versioned non-secret configuration for an existing project environment.
- `data.gregale_app` reads an existing app for adoption and resource composition.
- `data.gregale_deployment` reads an existing deployment for status and preview composition.
- `data.gregale_latest_deployment` reads the newest deployment for an app without requiring its ID.
- `data.gregale_private_network` reads an existing private network for attachment and peering composition.
- `gregale_project_environment` reads a durable project environment without
  copying secrets into Terraform state.

Project-environment creation and deletion are not exposed as a Terraform resource yet:
the Gregale API currently has no safe delete operation for environments. This
keeps Terraform destroy from silently leaving unmanaged remote state.

## Provider configuration

```hcl
terraform {
  required_providers {
    gregale = {
      source = "gregale/gregale"
    }
  }
}

provider "gregale" {
  # Prefer GREGALE_TOKEN in CI. The value is never stored in resource state.
  token    = var.gregale_token
  base_url = "https://api.gregale.dev"
}
```

Alert, environment variable, and secret resources use Terraform's write-only attribute support and
therefore require Terraform 1.11 or later.

## App example

```hcl
resource "gregale_app" "api" {
  slug             = "orders-api"
  resource_profile = "small"
  max_concurrency  = 8
  idle_timeout_s   = 60
  health_path      = "/healthz"
}
```

## Existing app lookup

Use the app data source when the app already exists or is managed outside
Terraform. It is read-only and can supply stable IDs and current runtime
metadata to other resources.

```hcl
data "gregale_app" "api" {
  slug = "orders-api"
}

resource "gregale_domain" "api" {
  domain = "api.example.com"
  app_id = data.gregale_app.api.id
}

output "app_url" {
  value = data.gregale_app.api.url
}
```

## Existing deployment lookup

Use the deployment data source when a deployment was created by the CLI,
dashboard, CI, or another Terraform stack. It is read-only and exposes current
status, resolved commit metadata, structured failure details, and the preview
URL.

```hcl
data "gregale_deployment" "release" {
  deployment_id = var.deployment_id
}

output "preview_url" {
  value = data.gregale_deployment.release.preview_url
}
```

## Latest deployment lookup

Use the latest deployment data source when a release was created by the CLI,
dashboard, CI, or another Terraform stack and the deployment ID is not known
ahead of time. Gregale resolves the newest deployment by creation time.

```hcl
data "gregale_latest_deployment" "api" {
  app_slug = "orders-api"
}

output "preview_url" {
  value = data.gregale_latest_deployment.api.preview_url
}
```

## Custom domain

```hcl
resource "gregale_domain" "api" {
  domain = "api.example.com"
  app_id = gregale_app.api.app_id
}

output "domain_txt_record" {
  value     = gregale_domain.api.txt_record
  sensitive = true
}
```

## Raw TCP listener

Expose a declared app TCP port through a stable public Gregale port. Gregale
allocates a port from `40000`–`49999` when `public_port` is omitted; the port
survives instance wake, migration, and redeployment.

```hcl
resource "gregale_tcp_listener" "postgres" {
  app_slug   = gregale_app.api.slug
  name       = "postgres"
  guest_port = 5432
}

output "postgres_public_port" {
  value = gregale_tcp_listener.postgres.public_port
}
```

## Static egress IP

Pin a provisioned public IPv4 address so databases and third-party APIs can
allowlist the app's outbound traffic. Static egress IPs require a plan that
supports the feature and survive app scale-to-zero.

```hcl
resource "gregale_static_egress_ip" "api" {
  app_slug = gregale_app.api.slug
  ip       = var.api_egress_ip
}
```

## Private-network attachment

Create a provider-neutral private network and attach an app to it. Gregale
accepts attachment requests asynchronously, so `status` remains `pending`
until a connector reports `ready`; pending and error states remain fail-closed
for traffic.

```hcl
resource "gregale_private_network" "prod" {
  name          = "production"
  region        = "fra1"
  cidr          = "10.20.0.0/16"
  allowed_cidrs = ["10.20.0.0/24"]
}

resource "gregale_private_network_attachment" "api" {
  app_slug   = gregale_app.api.slug
  network_id = gregale_private_network.prod.id
  region     = gregale_private_network.prod.region
  cidrs      = [gregale_private_network.prod.cidr]
}

output "private_address" {
  value = gregale_private_network_attachment.api.address
}
```

`allowed_cidrs` is optional. When set, it narrows private traffic to the
listed IPv4 ranges, which must be contained by the network CIDR; changing the
policy updates the network in place. `firewall_rules` is also optional and
supports protocol/port rules for ingress and egress. Omit both to preserve
allow-all behavior.

```hcl
resource "gregale_private_network" "prod" {
  name   = "production"
  region = "fra1"
  cidr   = "10.20.0.0/16"

  firewall_rules = [{
    direction = "ingress"
    protocol  = "tcp"
    cidrs     = ["10.20.0.0/24"]
    ports     = ["443", "8000-8080"]
  }]
}
```

TCP and UDP rules require at least one port or inclusive range. ICMP rules
omit `ports`; an empty `cidrs` list means the whole private network CIDR.

## Existing private-network lookup

Use the private-network data source when a network is owned by another
Terraform stack, the CLI, or the dashboard. It exposes the canonical network
ID, region, CIDR, status, and complete network policy for composing attachments
and peerings without duplicating lifecycle ownership.

```hcl
data "gregale_private_network" "shared" {
  network_id = var.network_id
}

resource "gregale_private_network_attachment" "api" {
  app_slug   = "orders-api"
  network_id = data.gregale_private_network.shared.id
  region     = data.gregale_private_network.shared.region
  cidrs      = [data.gregale_private_network.shared.cidr]
}
```

## Private-network peering

Connect two Gregale-owned private networks in the same account and region.
Peering is symmetric and converges asynchronously; Terraform exposes the
`pending`, `ready`, or `error` status reported by the fabric.

```hcl
resource "gregale_private_network_peering" "app_data" {
  network_id      = gregale_private_network.app.id
  peer_network_id = gregale_private_network.data.id
}

output "peering_status" {
  value = gregale_private_network_peering.app_data.status
}
```

## Alert rule

```hcl
resource "gregale_alert" "latency" {
  app_slug       = gregale_app.api.slug
  name           = "p95-latency"
  metric         = "latency_p95_ms"
  comparison     = "gt"
  threshold      = 500
  window_spec    = "5m"
  webhook_url    = var.alert_webhook_url
  webhook_secret = var.alert_webhook_secret
}
```

The webhook secret is sent on create/update but is never written to the plan
or state. Gregale returns only `webhook_secret_masked`; Terraform refreshes
the rule's `state`, firing timestamps, and delivery configuration.

## Scheduled invocation

```hcl
resource "gregale_cron" "sync" {
  app_id         = gregale_app.api.app_id
  schedule       = "*/15 * * * *"
  path           = "/internal/sync"
  timezone       = "UTC"
  skip_if_running = true
}
```

The schedule uses the standard five-field cron format. Gregale evaluates it
in the configured IANA timezone and reports `last_fired_at` and any
`suspended_reason` during refresh.

## App secret

```hcl
resource "gregale_secret" "database_url" {
  app_slug = gregale_app.api.slug
  key      = "DATABASE_URL"
  value    = var.database_url
  scope    = "production"
}
```

The value is sent only to the secret write endpoint and is never returned by
Gregale or stored in the Terraform plan/state. Refresh reads only timestamps,
the sealing key identity, and an opaque value fingerprint. Omitting `scope`
uses Gregale's `default` scope.

## App environment variable

```hcl
resource "gregale_env" "log_level" {
  app_slug = gregale_app.api.slug
  key      = "LOG_LEVEL"
  value    = "info"
  scope    = "production"
}
```

The value is sent only to the env write endpoint and is never returned by
Gregale or stored in the Terraform plan/state. Use `gregale_secret` for
credentials; `gregale_env` is intended for non-sensitive runtime
configuration. Omitting `scope` uses Gregale's `default` scope.

## Deployment

```hcl
resource "gregale_deployment" "api" {
  app_slug    = gregale_app.api.slug
  repo        = "acme/orders-api"
  ref         = var.git_ref
  environment = "production"
}
```

For CI pipelines that build and publish an image first, use the same resource
with a digest-pinned OCI image instead of `repo` and `ref`:

```hcl
resource "gregale_deployment" "api" {
  app_slug    = gregale_app.api.slug
  image       = var.image_digest
  environment = "production"
}
```

Set exactly one deployment source: either `image`, or both `repo` and `ref`.

The resource deploys the source ref through Gregale's headless GitHub path,
or submits the OCI image to Gregale, waits for the deployment to become live,
and exposes the resolved commit, preview URL, stage state, and structured
failure details. Deployment inputs are immutable; changing them creates a new
deployment. Destroy cancels only a deployment that is still queued or building
and never rolls back a live app.

Creating the resource returns the DNS challenge immediately. Gregale verifies
DNS and provisions TLS asynchronously; refresh the resource to observe
`verification_status`, `cert_status`, and certificate expiry without making
`terraform apply` wait for DNS propagation.

## Environment lookup

```hcl
data "gregale_project_environment" "production" {
  project_slug = "orders"
  slug         = "production"
}

output "production_environment_id" {
  value = data.gregale_project_environment.production.project_id
}
```

The provider uses the same public REST contract as the CLI and sends
idempotency keys for app, domain, alert, cron, environment variable, secret, and deployment writes. Application
environment values, secret values, alert webhook secrets, managed secret values, and bearer tokens
are not returned by the managed resources or recorded in state.

## Project environment configuration

Manage a durable environment's non-secret JSON configuration. The environment
must already exist, and Gregale rejects secret-shaped keys; use
`gregale_secret` for credentials. Configuration is canonicalized by Gregale,
so `jsonencode` keeps Terraform plans stable. Destroying this resource resets
the environment configuration to `{}`; it does not delete the environment.

```hcl
resource "gregale_project_environment_config" "production" {
  project_slug = "orders"
  environment  = "production"
  values = jsonencode({
    region   = "eu"
    replicas = 2
  })
}

output "config_version" {
  value = gregale_project_environment_config.production.version
}
```
