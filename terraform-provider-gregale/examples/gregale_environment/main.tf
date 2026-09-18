terraform {
  required_providers {
    gregale = {
      source = "gregale/gregale"
    }
  }
}

provider "gregale" {}

data "gregale_project_environment" "production" {
  project_slug = "orders"
  slug         = "production"
}

output "project_id" {
  value = data.gregale_project_environment.production.project_id
}
