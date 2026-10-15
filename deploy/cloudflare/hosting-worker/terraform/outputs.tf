output "hosting" {
  description = "Non-secret deployment identity consumed by the live qualification runner."
  value = {
    version         = 1
    account_id      = var.account_id
    hosting_domain  = var.hosting_domain
    bucket          = cloudflare_r2_bucket.artifacts.name
    r2_endpoint     = local.endpoint
    origin_id       = local.origin_id
    kv_namespace_id = cloudflare_workers_kv_namespace.routes.id
    worker_name     = cloudflare_worker.hosting.name
    worker_sha256   = filesha256(local.worker_source)
    worker_enabled  = var.worker_enabled
    route_attached  = var.attach_route
    access_team     = var.access_team_domain
    access_aud      = cloudflare_zero_trust_access_application.hosting.aud
    access_app_id   = cloudflare_zero_trust_access_application.hosting.id
  }
}

output "apid_environment" {
  description = "Non-secret bootstrap settings; inject scoped credentials separately through sealed environment configuration."
  value = {
    FAAS_FRONTEND_HOSTING_ENABLED         = "0"
    FAAS_FRONTEND_HOSTING_DOMAIN          = var.hosting_domain
    FAAS_FRONTEND_HOSTING_R2_ENDPOINT     = local.endpoint
    FAAS_FRONTEND_HOSTING_R2_BUCKET       = cloudflare_r2_bucket.artifacts.name
    FAAS_FRONTEND_HOSTING_CF_ACCOUNT_ID   = var.account_id
    FAAS_FRONTEND_HOSTING_KV_NAMESPACE_ID = cloudflare_workers_kv_namespace.routes.id
  }
}
