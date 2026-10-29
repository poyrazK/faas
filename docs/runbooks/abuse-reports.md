# Abuse reports

How to handle a third-party report that a customer application is used for
phishing, malware, spam or attacks. Reports arrive at `abuse@gregale.dev`
(customer page: [Report abuse](../abuse.md)). API-traffic abuse by a customer
against Gregale itself is [tenant-abuse](tenant-abuse.md); tenant egress
alerts are [FaasTenantEgressAbuse](FaasTenantEgressAbuse.md).

## 1. Identify the application

For `<slug>.gregale.dev`, the slug is the first label of the host. For any
other host, look up the custom domain first.

```sql
-- Custom domain to app:
SELECT d.app_id, d.domain, d.verified_at FROM custom_domains d WHERE d.domain = 'host.example.com';

-- App, its creator account and its organization:
SELECT a.id, a.slug, a.account_id, a.org_id, a.status FROM apps a WHERE a.slug = 'example-app';

-- Whether that organization is the creator's personal one:
SELECT o.id, o.personal_org, o.abuse_hold_at FROM orgs o WHERE o.id = '<org_id>';
```

Use the read-only database role. Record the app ID, account ID and org ID in
the report thread.

## 2. Verify

Open the reported URL from an isolated browser or with `curl -sS -D -` and keep
the response headers and body as evidence. Do not log in to anything the page
offers. A report about traffic needs the source address and time: check the
app's egress counters and recent deploys for the same window.

## 3. Suspend

Phishing, malware and attacks in progress are suspended first and
investigated after. Everything else gets a message to the customer and 48
hours to respond.

An abuse hold refuses boots and deploys from the moment it commits; schedd
parks the running workloads on its next reaper pass (ADR-361) and never
snapshots them, so a release does not restore the offending process.

Hold the subject that owns the app's resources:

- **Personal app** (`org_id` empty, or a personal org): hold `account_id`.
- **Company app** (a non-personal org): hold the **org ID**. The same endpoint
  sets the organization's hold when the ID is an organization billing
  subject. If it answers 404 `Account not found`, the organization has not
  adopted resource ownership yet: hold the creator's `account_id` instead.

```bash
curl -fsS -X POST "https://api.gregale.dev/v1/admin/accounts/$SUBJECT_ID/abuse-hold" \
  -H "Authorization: Bearer $OPERATOR_KEY" -H 'Content-Type: application/json' \
  -d '{"note":"abuse report <ticket>: phishing at example-app.gregale.dev"}'
```

The note must be 3 to 500 characters and is written to the audit log. Confirm
the app no longer serves (`curl` the URL again) before replying.

## 4. Reply and close

- Reporter: confirm the action taken, without naming the customer.
- Customer: say what was reported, what was suspended, and how to respond.
- If the customer resolves the issue, release the hold. Apps stay parked and
  wake on their next request.

```bash
curl -fsS -X DELETE "https://api.gregale.dev/v1/admin/accounts/$SUBJECT_ID/abuse-hold" \
  -H "Authorization: Bearer $OPERATOR_KEY"
```

Repeat abuse from the same customer is not released; close the account
through the normal account deletion path.
