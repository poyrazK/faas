terraform {
  required_providers {
    gregale = {
      source = "gregale/gregale"
    }
  }
}

provider "gregale" {
  token = var.gregale_token
}

variable "gregale_token" {
  type      = string
  sensitive = true
}

resource "gregale_project_environment" "preview" {
  project_slug = "orders"
  slug         = "preview"
  protected    = false
}

output "environment_id" {
  value = gregale_project_environment.preview.id
}
