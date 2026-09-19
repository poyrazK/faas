---
page_title: "gregale_private_network Resource - Gregale"
subcategory: ""
description: |-
  Manages a Gregale-owned provider-neutral private network.
---

# gregale_private_network (Resource)

Creates a Gregale-owned provider-neutral private network that apps can attach
to for private ingress and egress. The CIDR must be an RFC1918 IPv4 range
between `/16` and `/28`; the region is a Gregale placement label, not a
provider-specific region identifier.

Network name, region, and CIDR are immutable and changing any of them creates
a replacement. A network cannot be deleted while an app remains attached.

## Example Usage

```terraform
resource "gregale_private_network" "prod" {
  name   = "production"
  region = "fra1"
  cidr   = "10.20.0.0/16"
}
```

## Schema

### Required

- `name` (String) Stable private network name. Changing it forces replacement.
- `region` (String) Gregale placement region. Changing it forces replacement.
- `cidr` (String) RFC1918 IPv4 network range from `/16` through `/28`. Changing it forces replacement.

### Read-only

- `id` (String) Stable Gregale private network identifier.
- `status` (String) Network status: `ready` or `error`.
- `status_detail` (String) Latest network provisioning detail.
- `created_at` (String) Network creation timestamp.
- `updated_at` (String) Network update timestamp.

## Import

Import uses the stable Gregale network ID:

```shell
terraform import gregale_private_network.prod prod-vpc
```
