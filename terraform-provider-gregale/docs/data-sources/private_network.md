page_title: "gregale_private_network Data Source - Gregale"
subcategory: ""
description: |-
  Reads an existing Gregale-owned private network.
---

# gregale_private_network (Data Source)

Reads an existing Gregale-owned provider-neutral private network without
managing its lifecycle. Use this data source when the network is owned by
another Terraform stack, the CLI, or the dashboard and an attachment or
peering needs its canonical ID and policy.

## Example Usage

```terraform
data "gregale_private_network" "shared" {
  network_id = var.network_id
}

resource "gregale_private_network_attachment" "api" {
  app_slug   = "orders-api"
  network_id = data.gregale_private_network.shared.id
  region     = data.gregale_private_network.shared.region
  cidrs      = [data.gregale_private_network.shared.cidr]
}

output "network_status" {
  value = data.gregale_private_network.shared.status
}
```

## Schema

### Required

- `network_id` (String) Stable Gregale private network identifier to look up.

### Read-only

- `id` (String) Stable Gregale private network identifier.
- `name` (String) Stable private network name.
- `region` (String) Gregale placement region for the private network.
- `cidr` (String) RFC1918 IPv4 network range.
- `allowed_cidrs` (Set of String) Private IPv4 policy ranges admitted symmetrically for ingress and egress.
- `firewall_rules` (List of Object) Protocol and port allow rules applied to every attachment. Each object contains `direction`, `protocol`, `cidrs`, and `ports`.
- `status` (String) Network status: `ready` or `error`.
- `status_detail` (String) Latest private network provisioning detail.
- `created_at` (String) RFC3339 network creation timestamp.
- `updated_at` (String) RFC3339 network update timestamp.
