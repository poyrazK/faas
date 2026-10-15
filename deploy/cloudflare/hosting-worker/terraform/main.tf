data "cloudflare_zone" "hosting" {
  zone_id = var.zone_id
}

locals {
  endpoint      = "https://${var.account_id}.r2.cloudflarestorage.com"
  bucket        = "${var.name}-artifacts"
  origin_id     = sha256("${local.endpoint}\n${local.bucket}")
  worker_source = "${path.module}/../worker.mjs"
}

resource "terraform_data" "isolation" {
  input = var.hosting_domain
  lifecycle {
    precondition {
      condition     = data.cloudflare_zone.hosting.name == var.hosting_domain
      error_message = "hosting_domain must equal the dedicated zone's apex."
    }
    precondition {
      condition     = data.cloudflare_zone.hosting.account.id == var.account_id
      error_message = "The isolated hosting zone and Worker resources must belong to the same account."
    }
    precondition {
      condition     = alltrue([for domain in var.platform_domains : var.hosting_domain != domain && !endswith(var.hosting_domain, ".${domain}") && !endswith(domain, ".${var.hosting_domain}")])
      error_message = "Use a different registrable domain from the platform; apid also enforces public-suffix isolation."
    }
    precondition {
      condition     = !var.worker_enabled || var.attach_route
      error_message = "Attach the isolated route before enabling the edge feature."
    }
  }
}

resource "cloudflare_r2_bucket" "artifacts" {
  account_id    = var.account_id
  name          = local.bucket
  jurisdiction  = "default"
  storage_class = "Standard"
  depends_on    = [terraform_data.isolation]
  lifecycle {
    prevent_destroy = true
  }
}

resource "cloudflare_r2_managed_domain" "private" {
  account_id  = var.account_id
  bucket_name = cloudflare_r2_bucket.artifacts.name
  enabled     = false
}

# Only abandoned staging objects expire automatically. Published releases are
# removed by Gregale after routing leases and upload grants have expired.
resource "cloudflare_r2_bucket_lifecycle" "staging" {
  account_id  = var.account_id
  bucket_name = cloudflare_r2_bucket.artifacts.name
  rules = [{
    id         = "expire-abandoned-hosting-staging"
    enabled    = true
    conditions = { prefix = "hosting/staging/" }
    delete_objects_transition = {
      condition = { type = "Age", max_age = 86400 }
    }
  }]
}

resource "cloudflare_workers_kv_namespace" "routes" {
  account_id = var.account_id
  title      = "${var.name}-routes"
  depends_on = [terraform_data.isolation]
}

resource "cloudflare_zero_trust_access_policy" "internal" {
  account_id       = var.account_id
  name             = "${var.name}-internal-identities"
  decision         = "allow"
  session_duration = "1h"
  include          = [for email in sort(tolist(var.internal_emails)) : { email = { email = email } }]
  depends_on       = [terraform_data.isolation]
}

resource "cloudflare_zero_trust_access_application" "hosting" {
  account_id                 = var.account_id
  name                       = var.name
  domain                     = "*.${var.hosting_domain}"
  type                       = "self_hosted"
  session_duration           = "1h"
  app_launcher_visible       = false
  http_only_cookie_attribute = true
  same_site_cookie_attribute = "lax"
  options_preflight_bypass   = false
  policies = [{
    id         = cloudflare_zero_trust_access_policy.internal.id
    precedence = 1
  }]
}

resource "cloudflare_worker" "hosting" {
  account_id = var.account_id
  name       = var.name
  subdomain = {
    enabled          = false
    previews_enabled = false
  }
  depends_on = [terraform_data.isolation]
}

resource "cloudflare_worker_version" "hosting" {
  account_id         = var.account_id
  worker_id          = cloudflare_worker.hosting.id
  compatibility_date = "2026-10-15"
  main_module        = "worker.mjs"
  modules = [{
    name         = "worker.mjs"
    content_file = local.worker_source
    content_type = "application/javascript+module"
  }]
  # Explicitly include the content digest so code edits always create a version.
  annotations = { workers_tag = substr(filesha256(local.worker_source), 0, 32) }
  bindings = concat([
    { name = "ARTIFACTS", type = "r2_bucket", bucket_name = cloudflare_r2_bucket.artifacts.name },
    { name = "ROUTES", type = "kv_namespace", namespace_id = cloudflare_workers_kv_namespace.routes.id },
    ], [for name, text in {
      FAAS_FRONTEND_HOSTING_ENABLED                 = tostring(var.worker_enabled)
      FAAS_FRONTEND_HOSTING_DURABLE_ROUTING_ENABLED = tostring(var.durable_routing_enabled)
      ACCESS_TEAM_DOMAIN                            = var.access_team_domain
      ACCESS_AUD                                    = cloudflare_zero_trust_access_application.hosting.aud
      INTERNAL_EMAILS                               = join(",", sort(tolist(var.internal_emails)))
      ORIGIN_ID                                     = local.origin_id
  } : { name = name, type = "plain_text", text = text }])
  depends_on = [cloudflare_r2_managed_domain.private, cloudflare_r2_bucket_lifecycle.staging]
}

resource "cloudflare_workers_deployment" "hosting" {
  account_id  = var.account_id
  script_name = cloudflare_worker.hosting.name
  strategy    = "percentage"
  versions = [{
    percentage = 100
    version_id = cloudflare_worker_version.hosting.id
  }]
}

# A reserved, non-serving origin avoids forwarding misses to a Gregale node.
# No DNS record or route exists until attach_route is explicitly selected.
resource "cloudflare_dns_record" "hosting" {
  count   = var.attach_route ? 1 : 0
  zone_id = var.zone_id
  name    = "*.${var.hosting_domain}"
  type    = "A"
  content = "192.0.2.1"
  proxied = true
  ttl     = 1
  depends_on = [
    cloudflare_zero_trust_access_application.hosting,
    cloudflare_workers_deployment.hosting,
  ]
}

resource "cloudflare_workers_route" "hosting" {
  count      = var.attach_route ? 1 : 0
  zone_id    = var.zone_id
  pattern    = "*.${var.hosting_domain}/*"
  script     = cloudflare_worker.hosting.name
  depends_on = [cloudflare_dns_record.hosting]
}
