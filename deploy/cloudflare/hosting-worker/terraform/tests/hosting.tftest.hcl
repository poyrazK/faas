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
    condition     = !var.custom_domains_enabled && length(cloudflare_custom_hostname_fallback_origin.hosting) == 0 && length(cloudflare_dns_record.saas) == 0 && output.apid_environment.FAAS_FRONTEND_HOSTING_DOMAINS_ENABLED == "0"
    error_message = "Custom domains and SaaS resources must be separately default-off."
  }
  assert {
    condition     = length(cloudflare_r2_bucket_cors.dashboard.rules) == 0
    error_message = "Default infrastructure must not permit browser uploads."
  }
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
    condition     = output.apid_environment.FAAS_FRONTEND_HOSTING_ENABLED == "0" && output.apid_environment.FAAS_FRONTEND_HOSTING_AUTOMATION_ENABLED == "0"
    error_message = "Bootstrap output must leave API admission disabled."
  }
  assert {
    condition     = !output.hosting.durable_routing_enabled && one([for binding in cloudflare_worker_version.hosting.bindings : binding.text if binding.name == "FAAS_FRONTEND_HOSTING_DURABLE_ROUTING_ENABLED"]) == "false"
    error_message = "Durable routing must require an independent explicit opt-in."
  }
}

run "custom_domain_opt_in" {
  command = plan
  variables {
    attach_route             = true
    durable_routing_enabled  = true
    configure_custom_domains = true
    custom_domains_enabled   = true
  }
  assert {
    condition     = cloudflare_workers_route.hosting[0].pattern == "*/*" && cloudflare_dns_record.saas[0].name == "customers.gregale-hosting.example" && cloudflare_dns_record.saas[0].content == "192.0.2.1" && cloudflare_dns_record.saas[0].proxied
    error_message = "SaaS traffic must terminate at the isolated Worker, never at Gregale servers."
  }
  assert {
    condition     = one([for binding in cloudflare_worker_version.hosting.bindings : binding.text if binding.name == "FAAS_FRONTEND_HOSTING_DOMAINS_ENABLED"]) == "true" && output.apid_environment.FAAS_FRONTEND_HOSTING_DOMAINS_ENABLED == "0"
    error_message = "Worker custom routing must not enable API admission."
  }
}
run "reject_custom_domains_without_durable_route" {
  command = plan
  variables { custom_domains_enabled = true }
  expect_failures = [terraform_data.isolation]
}

run "domain_serving_off_preserves_fallback" {
  command = plan
  variables {
    attach_route             = true
    durable_routing_enabled  = true
    configure_custom_domains = true
    custom_domains_enabled   = false
  }
  assert {
    condition     = length(cloudflare_custom_hostname_fallback_origin.hosting) == 1 && cloudflare_workers_route.hosting[0].pattern == "*/*" && one([for binding in cloudflare_worker_version.hosting.bindings : binding.text if binding.name == "FAAS_FRONTEND_HOSTING_DOMAINS_ENABLED"]) == "false"
    error_message = "Stopping custom-domain serving must not destroy its fallback or route."
  }
}

run "explicit_dashboard_uploads" {
  command = plan
  variables { dashboard_upload_origins = ["https://operations.gregale.dev"] }
  assert {
    condition     = cloudflare_r2_bucket_cors.dashboard.rules[0].allowed.origins == tolist(["https://operations.gregale.dev"]) && cloudflare_r2_bucket_cors.dashboard.rules[0].allowed.methods == tolist(["PUT"]) && cloudflare_r2_bucket_cors.dashboard.rules[0].allowed.headers == tolist(["content-type"])
    error_message = "Browser CORS must permit only exact origins and the upload method/header."
  }
  assert {
    condition     = !cloudflare_r2_managed_domain.private.enabled && !var.worker_enabled && !var.attach_route
    error_message = "Browser CORS must not make storage public or enable site serving."
  }
}

run "reject_wildcard_upload_origin" {
  command = plan
  variables { dashboard_upload_origins = ["https://*.gregale.dev"] }
  expect_failures = [var.dashboard_upload_origins]
}

run "reject_http_upload_origin" {
  command = plan
  variables { dashboard_upload_origins = ["http://operations.gregale.dev"] }
  expect_failures = [var.dashboard_upload_origins]
}

run "reject_upload_origin_path" {
  command = plan
  variables { dashboard_upload_origins = ["https://operations.gregale.dev/hosting"] }
  expect_failures = [var.dashboard_upload_origins]
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

run "analytics_disabled_defaults" {
  command = plan
  assert {
    condition     = !var.configure_analytics && !var.analytics_enabled && length([for binding in cloudflare_worker_version.hosting.bindings : binding if binding.name == "ANALYTICS"]) == 0 && output.apid_environment.FAAS_FRONTEND_HOSTING_ANALYTICS_ENABLED == "0"
    error_message = "Analytics binding, collection and management must all remain default-off."
  }
}
run "analytics_binding_without_collection" {
  command = plan
  variables { configure_analytics = true }
  assert {
    condition     = one([for binding in cloudflare_worker_version.hosting.bindings : binding.dataset if binding.name == "ANALYTICS"]) == "gregale_internal_hosting_traffic" && one([for binding in cloudflare_worker_version.hosting.bindings : binding.text if binding.name == "FAAS_FRONTEND_HOSTING_ANALYTICS_ENABLED"]) == "false" && output.apid_environment.FAAS_FRONTEND_HOSTING_ANALYTICS_DATASET == "gregale_internal_hosting_traffic"
    error_message = "Binding preparation must not start collection or enable API reads."
  }
}
run "analytics_collection_requires_binding" {
  command = plan
  variables { analytics_enabled = true }
  expect_failures = [terraform_data.isolation]
}
run "analytics_opt_in" {
  command = plan
  variables {
    configure_analytics = true
    analytics_enabled   = true
  }
  assert {
    condition     = output.hosting.analytics_enabled && one([for binding in cloudflare_worker_version.hosting.bindings : binding.text if binding.name == "FAAS_FRONTEND_HOSTING_ANALYTICS_ENABLED"]) == "true" && output.apid_environment.FAAS_FRONTEND_HOSTING_ANALYTICS_ENABLED == "0"
    error_message = "Worker collection and API read admission must be independent."
  }
}

run "release_config_default_off" {
  command = plan
  assert {
    condition     = !var.release_config_enabled && one([for binding in cloudflare_worker_version.hosting.bindings : binding.text if binding.name == "FAAS_FRONTEND_HOSTING_CONFIG_ENABLED"]) == "false" && output.apid_environment.FAAS_FRONTEND_HOSTING_CONFIG_ENABLED == "0"
    error_message = "Release configuration admission and serving must default off."
  }
}
run "release_config_independent_serving_gate" {
  command = plan
  variables { release_config_enabled = true }
  assert {
    condition     = output.hosting.release_config_enabled && !output.hosting.worker_enabled && output.apid_environment.FAAS_FRONTEND_HOSTING_CONFIG_ENABLED == "0" && one([for binding in cloudflare_worker_version.hosting.bindings : binding.text if binding.name == "FAAS_FRONTEND_HOSTING_CONFIG_ENABLED"]) == "true"
    error_message = "Release configuration must not enable API admission or general serving."
  }
}

run "private_previews_default_off" {
  command = plan
  assert {
    condition     = !var.previews_enabled && one([for binding in cloudflare_worker_version.hosting.bindings : binding.text if binding.name == "FAAS_FRONTEND_HOSTING_PREVIEWS_ENABLED"]) == "false" && output.apid_environment.FAAS_FRONTEND_HOSTING_PREVIEWS_ENABLED == "0"
    error_message = "Private previews must default off independently at API and edge."
  }
}
run "private_previews_independent_gate" {
  command = plan
  variables { previews_enabled = true }
  assert {
    condition     = output.hosting.previews_enabled && !output.hosting.worker_enabled && output.apid_environment.FAAS_FRONTEND_HOSTING_PREVIEWS_ENABLED == "0" && one([for binding in cloudflare_worker_version.hosting.bindings : binding.text if binding.name == "FAAS_FRONTEND_HOSTING_PREVIEWS_ENABLED"]) == "true"
    error_message = "Preview serving must not enable API admission or the main worker gate."
  }
}

run "release_history_api_default_off" {
  command = plan
  assert {
    condition     = output.apid_environment.FAAS_FRONTEND_HOSTING_HISTORY_ENABLED == "0"
    error_message = "History/provenance admission must remain an explicit API opt-in."
  }
}

run "retention_api_default_off" {
  command = plan
  assert {
    condition     = output.apid_environment.FAAS_FRONTEND_HOSTING_RETENTION_ENABLED == "0"
    error_message = "Infrastructure preparation must not enable automatic retirement."
  }
}

run "oidc_stays_off" {
  command = plan
  assert {
    condition     = output.apid_environment.FAAS_FRONTEND_HOSTING_OIDC_ENABLED == "0"
    error_message = "Provider wiring must never enable GitHub identity exchange."
  }
}

run "diagnostics_api_default_off" {
  command = plan
  assert {
    condition     = output.apid_environment.FAAS_FRONTEND_HOSTING_DIAGNOSTICS_ENABLED == "0"
    error_message = "Provider wiring must leave stored deployment diagnostics disabled."
  }
}

run "ci_setup_default_off" {
  command = plan
  assert {
    condition     = output.apid_environment.FAAS_FRONTEND_HOSTING_CI_SETUP_ENABLED == "0"
    error_message = "Guided GitHub setup must default off independently of serving and deployment."
  }
}

run "activity_default_off" {
  command = plan
  assert {
    condition     = output.apid_environment.FAAS_FRONTEND_HOSTING_ACTIVITY_ENABLED == "0"
    error_message = "Deployment activity must default off independently of serving and automation."
  }
}

run "cancellation_default_off" {
  command = plan

  assert {
    condition     = output.apid_environment.FAAS_FRONTEND_HOSTING_CANCELLATION_ENABLED == "0"
    error_message = "CI cancellation must require its independent operator opt-in."
  }
}
