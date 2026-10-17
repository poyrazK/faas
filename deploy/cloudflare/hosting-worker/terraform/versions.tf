terraform {
  required_version = ">= 1.7, < 2.0"

  required_providers {
    cloudflare = {
      source  = "cloudflare/cloudflare"
      version = "5.10.1"
    }
  }
}

# Supply CLOUDFLARE_API_TOKEN in the operator environment, never in tfvars.
provider "cloudflare" {}
