variable "account_id" {
  type        = string
  description = "Cloudflare account containing this dedicated pilot."
  validation {
    condition     = can(regex("^[0-9a-f]{32}$", var.account_id))
    error_message = "account_id must be a Cloudflare account identifier."
  }
}

variable "zone_id" {
  type        = string
  description = "Dedicated hosting zone, separate from platform application domains."
  validation {
    condition     = can(regex("^[0-9a-f]{32}$", var.zone_id))
    error_message = "zone_id must be a Cloudflare zone identifier."
  }
}

variable "hosting_domain" {
  type        = string
  description = "Lowercase apex of the dedicated hosting zone."
  validation {
    condition     = length(var.hosting_domain) <= 253 && can(regex("^([a-z0-9]([a-z0-9-]*[a-z0-9])?\\.)+[a-z]{2,}$", var.hosting_domain))
    error_message = "hosting_domain must be a lowercase DNS name without a wildcard."
  }
}

variable "platform_domains" {
  type        = set(string)
  description = "Registrable platform/dashboard/API domains that must never host customer HTML."
  default     = ["gregale.dev"]
  validation {
    condition     = length(var.platform_domains) > 0 && alltrue([for domain in var.platform_domains : length(domain) > 0 && domain == lower(trimspace(domain))])
    error_message = "Supply the lowercase registrable domains of the existing platform."
  }
}

variable "name" {
  type        = string
  description = "Unique resource prefix for this internal pilot."
  default     = "gregale-internal-hosting"
  validation {
    condition     = can(regex("^gregale-internal-[a-z0-9-]{1,30}[a-z0-9]$", var.name))
    error_message = "Use a gregale-internal- prefix and a short lowercase resource name."
  }
}

variable "access_team_domain" {
  type        = string
  description = "Existing Zero Trust organization domain; this configuration does not create an IdP."
  validation {
    condition     = can(regex("^[a-z0-9-]+\\.cloudflareaccess\\.com$", var.access_team_domain))
    error_message = "Use the organization's team.cloudflareaccess.com domain."
  }
}

variable "internal_emails" {
  type        = set(string)
  description = "Explicit internal identities permitted by both Access and the Worker."
  validation {
    condition     = length(var.internal_emails) > 0 && alltrue([for email in var.internal_emails : can(regex("^[a-z0-9.!#$%&'*+/=?^_`{|}~-]+@[a-z0-9.-]+\\.[a-z]{2,}$", email))])
    error_message = "An explicit, nonempty set of lowercase internal email addresses is required."
  }
}

variable "attach_route" {
  type        = bool
  description = "Attach wildcard DNS and the Worker route only after reviewing the isolated zone and Access policy."
  default     = false
}

variable "worker_enabled" {
  type        = bool
  description = "Independent edge feature switch; the API flag remains separately controlled."
  default     = false
}

variable "durable_routing_enabled" {
  type        = bool
  description = "Allow durable R2 routing heads; apid separately opts new sites into this routing mode."
  default     = false
}

variable "custom_domains_enabled" {
  type        = bool
  description = "Independent internal custom-hostname serving opt-in; requires an already enabled Cloudflare for SaaS zone with custom metadata support."
  default     = false
}

variable "configure_custom_domains" {
  type        = bool
  description = "Provision the isolated SaaS fallback and zone-wide Worker route. Drain domains before removing this infrastructure opt-in."
  default     = false
}

variable "dashboard_upload_origins" {
  type        = set(string)
  description = "Exact HTTPS operations-console origins allowed to PUT signed staging uploads. Empty disables browser CORS."
  default     = []
  validation {
    condition = alltrue([for origin in var.dashboard_upload_origins :
      can(regex("^https://([a-z0-9]([a-z0-9-]*[a-z0-9])?\\.)+[a-z]{2,}$", origin))
    ])
    error_message = "Use exact lowercase HTTPS origins without wildcards, ports, paths, credentials, or trailing slashes."
  }
}

variable "configure_analytics" {
  type        = bool
  default     = false
  description = "Attach a dedicated Analytics Engine binding; no dataset is created until the first write."
}
variable "analytics_enabled" {
  type        = bool
  default     = false
  description = "Collect privacy-limited, best-effort site traffic observations."
}

variable "release_config_enabled" {
  type        = bool
  default     = false
  description = "Serve immutable release redirects, approved headers and custom 404 pages; API admission remains independently disabled."
}

variable "previews_enabled" {
  description = "Serve private immutable previews. Independent of API admission; default off."
  type        = bool
  default     = false
}
