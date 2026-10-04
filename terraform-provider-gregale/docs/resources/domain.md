---
page_title: "gregale_domain Resource - Gregale"
subcategory: ""
description: |-
  Manages a custom hostname binding and observes DNS/TLS verification.
---

# gregale_domain (Resource)

Manages a custom hostname binding for a Gregale app. Creation is intentionally
non-blocking: the API returns the DNS challenge immediately while its verifier
and certificate lifecycle continue asynchronously.

## Example Usage

```terraform
resource "gregale_domain" "api" {
  domain = "api.example.com"
  app_id = gregale_app.api.app_id
}
```

Publish `txt_record` at the returned DNS name before expecting verification.
The challenge fields are marked sensitive because they are short-lived control
values, even though the TXT record itself must be public in DNS.

Point the domain at Gregale with the records in `routing_records`. Use the
`CNAME`; at a zone apex, where a CNAME is not allowed, use the `A`/`AAAA`
records marked `alternative` instead. For example, with a DNS provider that
takes one record per resource:

```terraform
resource "example_dns_record" "api" {
  name  = gregale_domain.api.routing_records[0].name
  type  = gregale_domain.api.routing_records[0].type
  value = gregale_domain.api.routing_records[0].value
}
```

## Schema

### Required

- `domain` (String) Custom hostname. Changing it forces replacement.
- `app_id` (String) Stable Gregale app identifier. Changing it forces replacement.

### Read-only

- `challenge_token` (String, Sensitive) DNS verification challenge token.
- `txt_record` (String, Sensitive) Complete TXT record value.
- `verified` (Boolean) Whether DNS verification completed.
- `verification_status` (String) `pending` or `verified`.
- `verified_at` (String) Verification timestamp.
- `default` (Boolean) Whether this is the app's default custom domain.
- `cert_status` (String) Durable certificate lifecycle status.
- `cert_expires_at` (String) Certificate expiry timestamp.
- `cert_sans` (List of String) Certificate DNS names.
- `cert_last_error` (String) Latest certificate error.
- `dns_last_checked_at` (String) Latest DNS observation timestamp.
- `routing_records` (Attributes List) DNS records that route the domain to Gregale. (see [below for nested schema](#nestedatt--routing_records))

<a id="nestedatt--routing_records"></a>
### Nested Schema for `routing_records`

Read-Only:

- `type` (String) Record type: `CNAME`, `A` or `AAAA`.
- `name` (String) Record name.
- `value` (String) Record value.
- `alternative` (Boolean) True for `A`/`AAAA` records that replace the CNAME at a zone apex.

## Import

Domains are imported by hostname:

```shell
terraform import gregale_domain.api api.example.com
```
