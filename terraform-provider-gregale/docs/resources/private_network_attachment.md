---
page_title: "gregale_private_network_attachment Resource - Gregale"
subcategory: ""
description: |-
  Attaches a Gregale app to a provider-neutral private network.
---

# gregale_private_network_attachment (Resource)

Requests a provider-neutral private-network attachment for an app. Gregale
accepts the intent asynchronously: the resource is initially `pending` and
becomes `ready` after a connector reconciles it. `pending` and `error` are
fail-closed, so private traffic is not admitted until the attachment is ready.

Changing attachment settings updates the intent in place. Changing the app
slug creates a replacement. The referenced network must already exist and be
owned by the account.

## Example Usage

```terraform
resource "gregale_private_network_attachment" "api" {
  app_slug   = "orders-api"
  network_id = "prod-vpc"
  region     = "fra1"
  cidrs      = ["10.30.0.0/16"]
}
```

## Schema

### Required

- `app_slug` (String) Slug of the Gregale app. Changing it forces replacement.
- `network_id` (String) Provider-neutral private network identifier.
- `region` (String) Private network placement region.
- `cidrs` (Set of String) Private IPv4 destination CIDRs routed through the attachment.

### Optional

- `allowed_cidrs` (Set of String) Optional private IPv4 policy ranges admitted symmetrically for ingress and egress. Empty preserves allow-all behavior.

### Read-only

- `attachment_id` (String) Stable attachment identifier.
- `address` (String) Stable app member IPv4 address when available.
- `status` (String) `pending`, `ready`, or `error`.
- `status_detail` (String) Latest reconciliation detail.
- `created_at` (String) Attachment creation timestamp.
- `updated_at` (String) Attachment update timestamp.
- `feature_enabled` (Boolean) Whether the cluster has enabled the feature.
- `plan_allowed` (Boolean) Whether the account plan permits attachments.
- `max_cidrs` (Number) Plan CIDR limit.

## Import

Import uses the app slug. The current attachment is read from Gregale:

```shell
terraform import gregale_private_network_attachment.api orders-api
```
