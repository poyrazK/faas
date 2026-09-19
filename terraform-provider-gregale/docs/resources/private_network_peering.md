---
page_title: "gregale_private_network_peering Resource - Gregale"
subcategory: ""
description: |-
  Manages a provider-neutral peering between two Gregale private networks.
---

# gregale_private_network_peering (Resource)

Creates a durable, symmetric peering between two Gregale-owned private
networks in the same account and region. Gregale activates routes
asynchronously, so `status` starts as `pending` and remains fail-closed until
the fabric reports `ready`.

Changing either network identifier replaces the peering. The peering resource
does not manage either network's lifecycle.

## Example Usage

```terraform
resource "gregale_private_network_peering" "app_data" {
  network_id     = gregale_private_network.app.id
  peer_network_id = gregale_private_network.data.id
}
```

## Schema

### Required

- `network_id` (String) Stable Gregale private network identifier that owns this peering. Changing it forces replacement.
- `peer_network_id` (String) Stable Gregale private network identifier to peer with. Changing it forces replacement.

### Read-only

- `id` (String) Stable Gregale private network peering identifier.
- `region` (String) Gregale placement region shared by the peered networks.
- `status` (String) Peering status: `pending`, `ready`, or `error`.
- `status_detail` (String) Latest peering reconciliation detail.
- `created_at` (String) Peering creation timestamp.
- `updated_at` (String) Peering update timestamp.

## Import

Import uses the owning network ID and peering ID:

```shell
terraform import gregale_private_network_peering.app_data prod-vpc/peer-vpc-data
```
