terraform {
  required_providers {
    gregale = {
      source = "gregale/gregale"
    }
  }
}

provider "gregale" {}

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
