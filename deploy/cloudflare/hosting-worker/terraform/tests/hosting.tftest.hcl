mock_provider "cloudflare" {
  mock_data "cloudflare_zone" {
    defaults = {
      name    = "gregale-hosting.example"
      account = { id = "11111111111111111111111111111111" }
    }
  }
}

variables {
  account_id         = "11111111111111111111111111111111"
  zone_id            = "22222222222222222222222222222222"
  hosting_domain     = "gregale-hosting.example"
  access_team_domain = "internal.cloudflareaccess.com"
  internal_emails    = ["operator@example.com"]
}

run "disabled_defaults" {
  command = plan
  assert {
    condition     = !output.hosting.worker_enabled && !output.hosting.route_attached
    error_message = "Initial infrastructure must not enable serving or attach routing."
  }
  assert {
    condition     = length(cloudflare_workers_route.hosting) == 0 && length(cloudflare_dns_record.hosting) == 0
    error_message = "No default wildcard DNS or Worker route may exist."
  }
  assert {
    condition     = !cloudflare_r2_managed_domain.private.enabled && !cloudflare_worker.hosting.subdomain.enabled && !cloudflare_worker.hosting.subdomain.previews_enabled
    error_message = "Alternate public storage, workers.dev, and preview paths must stay disabled."
  }
  assert {
    condition     = cloudflare_r2_bucket_lifecycle.staging.rules[0].conditions.prefix == "hosting/staging/"
    error_message = "Automatic expiration must never apply to published artifacts."
  }
  assert {
    condition     = output.apid_environment.FAAS_FRONTEND_HOSTING_ENABLED == "0"
    error_message = "Bootstrap output must leave API admission disabled."
  }
  assert {
    condition     = !output.hosting.durable_routing_enabled && one([for binding in cloudflare_worker_version.hosting.bindings : binding.text if binding.name == "FAAS_FRONTEND_HOSTING_DURABLE_ROUTING_ENABLED"]) == "false"
    error_message = "Durable routing must require an independent explicit opt-in."
  }
}

run "explicit_enable" {
  command = plan
  variables {
    attach_route            = true
    worker_enabled          = true
    durable_routing_enabled = true
  }
  assert {
    condition     = cloudflare_workers_route.hosting[0].pattern == "*.gregale-hosting.example/*"
    error_message = "Routing must stay within the isolated hosting zone."
  }
  assert {
    condition     = one([for binding in cloudflare_worker_version.hosting.bindings : binding.text if binding.name == "FAAS_FRONTEND_HOSTING_ENABLED"]) == "true"
    error_message = "Only an explicit opt-in enables the edge flag."
  }
  assert {
    condition     = output.hosting.durable_routing_enabled && one([for binding in cloudflare_worker_version.hosting.bindings : binding.text if binding.name == "FAAS_FRONTEND_HOSTING_DURABLE_ROUTING_ENABLED"]) == "true"
    error_message = "The independent durable-routing opt-in must reach the Worker."
  }
  assert {
    condition     = one([for binding in cloudflare_worker_version.hosting.bindings : binding.text if binding.name == "ORIGIN_ID"]) == sha256("https://11111111111111111111111111111111.r2.cloudflarestorage.com\ngregale-internal-hosting-artifacts")
    error_message = "Edge and API placement fingerprints must agree."
  }
}

run "reject_enable_without_route" {
  command = plan
  variables { worker_enabled = true }
  expect_failures = [terraform_data.isolation]
}

run "reject_platform_domain" {
  command = plan
  variables { platform_domains = ["gregale-hosting.example"] }
  expect_failures = [terraform_data.isolation]
}

run "reject_wrong_zone" {
  command = plan
  variables { hosting_domain = "different.example" }
  expect_failures = [terraform_data.isolation]
}

run "reject_empty_allowlist" {
  command = plan
  variables { internal_emails = [] }
  expect_failures = [var.internal_emails]
}

run "reject_cross_account_zone" {
  command = plan
  variables { account_id = "33333333333333333333333333333333" }
  expect_failures = [terraform_data.isolation]
}
