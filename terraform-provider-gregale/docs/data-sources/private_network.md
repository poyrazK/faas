---
page_title: "gregale_private_network Data Source - Gregale"
subcategory: ""
description: |-
  Reads an existing Gregale-owned provider-neutral private network.
---

# gregale_private_network (Data Source)

Reads an existing Gregale-owned provider-neutral private network without
creating, updating, or deleting it. Use this data source to adopt a network
created by the Gregale CLI, dashboard, or another Terraform stack and compose
its current settings with private-network attachments.

## Example Usage

```terraform
data "gregale_private_network" "prod" {
  network_id = var.private_network_id
}

resource "gregale_private_network_attachment" "api" {
  app_slug   = "orders-api"
  network_id = data.gregale_private_network.prod.id
  region     = data.gregale_private_network.prod.region
  cidrs      = [data.gregale_private_network.prod.cidr]
}
```

## Schema

### Required

- `network_id` (String) Stable Gregale private network identifier to look up.

### Read-only

- `id` (String) Stable Gregale private network identifier.
- `name` (String) Stable private network name.
- `region` (String) Gregale placement region.
- `cidr` (String) RFC1918 IPv4 network range.
- `allowed_cidrs` (Set of String) Private IPv4 policy ranges admitted symmetrically for ingress and egress.
- `status` (String) Network status: `ready` or `error`.
- `status_detail` (String) Latest private network provisioning detail.
- `created_at` (String) Network creation timestamp.
- `updated_at` (String) Network update timestamp.
