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

## Import

Domains are imported by hostname:

```shell
terraform import gregale_domain.api api.example.com
```
