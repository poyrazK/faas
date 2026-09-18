---
page_title: "gregale_static_egress_ip Resource - Gregale"
subcategory: ""
description: |-
  Pins a stable public IPv4 address for a Gregale app's outbound traffic.
---

# gregale_static_egress_ip (Resource)

Pins a provisioned public IPv4 address for an app's outbound traffic. This is
useful when a database or third-party API requires an allowlisted source IP.
The pin survives app scale-to-zero. The account plan must allow static egress
IPs, and the address must be provisioned for the Gregale host.

Changing the app or IP creates a replacement. If the pin is cleared outside
Terraform, the resource is recreated during the next plan/apply.

## Example Usage

```terraform
resource "gregale_static_egress_ip" "api" {
  app_slug = "orders-api"
  ip       = "203.0.113.42"
}
```

## Schema

### Required

- `app_slug` (String) Slug of the Gregale app whose outbound traffic should use the pinned IP. Changing it forces replacement.
- `ip` (String) Provisioned public IPv4 address. Changing it forces replacement.

### Read-only

- `set_at` (String) Timestamp when Gregale pinned the IP.
- `plan_cap` (Number) Maximum number of static egress IPs allowed per app by the account plan.
- `plan_allowed` (Boolean) Whether the account plan permits static egress IPs.

## Import

Import uses the app slug. The current pinned IP is read from Gregale:

```shell
terraform import gregale_static_egress_ip.api orders-api
```
