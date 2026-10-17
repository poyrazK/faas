output "hosting" {
  description = "Non-secret deployment identity consumed by the live qualification runner."
  value = {
    version                   = 1
    account_id                = var.account_id
    hosting_domain            = var.hosting_domain
    bucket                    = cloudflare_r2_bucket.artifacts.name
    r2_endpoint               = local.endpoint
    origin_id                 = local.origin_id
    kv_namespace_id           = cloudflare_workers_kv_namespace.routes.id
    worker_name               = cloudflare_worker.hosting.name
    worker_sha256             = filesha256(local.worker_source)
    analytics_configured      = var.configure_analytics
    analytics_enabled         = var.analytics_enabled
    release_config_enabled    = var.release_config_enabled
    previews_enabled          = var.previews_enabled
    analytics_dataset         = var.configure_analytics ? local.analytics_dataset : ""
    worker_enabled            = var.worker_enabled
    durable_routing_enabled   = var.durable_routing_enabled
    custom_domains_configured = var.configure_custom_domains
    custom_domains_enabled    = var.custom_domains_enabled
    domain_cname_target       = var.configure_custom_domains ? "customers.${var.hosting_domain}" : ""
    route_attached            = var.attach_route
    dashboard_upload_origins  = sort(tolist(var.dashboard_upload_origins))
    access_team               = var.access_team_domain
    access_aud                = cloudflare_zero_trust_access_application.hosting.aud
    access_app_id             = cloudflare_zero_trust_access_application.hosting.id
  }
}

output "apid_environment" {
  description = "Non-secret bootstrap settings; inject scoped credentials separately through sealed environment configuration."
  value = {
    FAAS_FRONTEND_HOSTING_ANALYTICS_ENABLED            = "0"
    FAAS_FRONTEND_HOSTING_ANALYTICS_DATASET            = var.configure_analytics ? local.analytics_dataset : ""
    FAAS_FRONTEND_HOSTING_ENABLED                      = "0"
    FAAS_FRONTEND_HOSTING_DURABLE_ROUTING_ENABLED      = "0"
    FAAS_FRONTEND_HOSTING_DOMAINS_ENABLED              = "0"
    FAAS_FRONTEND_HOSTING_CANCELLATION_ENABLED         = "0"
    FAAS_FRONTEND_HOSTING_PREVIEW_RESERVATIONS_ENABLED = "0"
    FAAS_FRONTEND_HOSTING_ACTIVITY_ENABLED             = "0"
    FAAS_FRONTEND_HOSTING_CI_SETUP_ENABLED             = "0"
    FAAS_FRONTEND_HOSTING_DIAGNOSTICS_ENABLED          = "0"
    FAAS_FRONTEND_HOSTING_OIDC_ENABLED                 = "0"
    FAAS_FRONTEND_HOSTING_RETENTION_ENABLED            = "0"
    FAAS_FRONTEND_HOSTING_AUTOMATION_ENABLED           = "0"
    FAAS_FRONTEND_HOSTING_HISTORY_ENABLED              = "0"
    FAAS_FRONTEND_HOSTING_PREVIEWS_ENABLED             = "0"
    FAAS_FRONTEND_HOSTING_CONFIG_ENABLED               = "0"
    FAAS_FRONTEND_HOSTING_DOMAINS_ZONE_ID              = var.configure_custom_domains ? var.zone_id : ""
    FAAS_FRONTEND_HOSTING_DOMAINS_ACCESS_POLICY_ID     = var.configure_custom_domains ? cloudflare_zero_trust_access_policy.internal.id : ""
    FAAS_FRONTEND_HOSTING_DOMAINS_CNAME_TARGET         = var.configure_custom_domains ? "customers.${var.hosting_domain}" : ""
    FAAS_FRONTEND_HOSTING_RESERVED_DOMAINS             = join(",", sort(tolist(var.platform_domains)))
    FAAS_FRONTEND_HOSTING_DOMAIN                       = var.hosting_domain
    FAAS_FRONTEND_HOSTING_R2_ENDPOINT                  = local.endpoint
    FAAS_FRONTEND_HOSTING_R2_BUCKET                    = cloudflare_r2_bucket.artifacts.name
    FAAS_FRONTEND_HOSTING_CF_ACCOUNT_ID                = var.account_id
    FAAS_FRONTEND_HOSTING_KV_NAMESPACE_ID              = cloudflare_workers_kv_namespace.routes.id
  }
}
