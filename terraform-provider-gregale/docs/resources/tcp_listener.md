---
page_title: "gregale_tcp_listener Resource - Gregale"
subcategory: ""
description: |-
  Manages a stable public raw TCP listener for a Gregale app.
---

# gregale_tcp_listener (Resource)

Exposes one declared app TCP port through a stable Gregale public port. The
public port remains stable across instance wake, migration, and redeployment.
When `public_port` is omitted, Gregale allocates one from the reserved
`40000`–`49999` range.

The listener name and guest port must match a TCP listener declared by the app.
Terraform can enable or disable an existing listener in place; changing the
app, name, guest port, or configured public port creates a replacement.

## Example Usage

```terraform
resource "gregale_tcp_listener" "postgres" {
  app_slug   = "orders-api"
  name       = "postgres"
  guest_port = 5432
}
```

## Schema

### Required

- `app_slug` (String) Slug of the Gregale app that owns the listener. Changing it forces replacement.
- `name` (String) Stable listener name declared by the app. Changing it forces replacement.
- `guest_port` (Number) TCP port exposed by the workload. Changing it forces replacement.

### Optional

- `public_port` (Number) Stable public TCP port in the `40000`–`49999` range. Gregale allocates one when omitted; changing a configured value forces replacement.
- `enabled` (Boolean) Whether the edge accepts new TCP connections. Defaults to the enabled state returned by Gregale and can be changed in place.

### Read-only

- `id` (String) Stable Gregale TCP listener identifier.
- `protocol` (String) Listener protocol, currently `tcp`.
- `created_at` (String) Listener creation timestamp.
- `updated_at` (String) Listener update timestamp.

## Import

Import uses the app slug and listener name:

```shell
terraform import gregale_tcp_listener.postgres orders-api/postgres
```
