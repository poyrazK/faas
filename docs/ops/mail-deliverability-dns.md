# Operator runbook — Mail deliverability (SPF / DKIM / DMARC)

**Issue #246 closure · ADR-115 §D3 / §D5**
**Last reviewed:** 2026-08-29

This runbook is the operator-side checklist for getting Gregale
outbound mail to land in the customer's **Primary** tab rather
than **Promotions** or **Spam**. The deliverability story is
five layers — three DNS records, one verified domain at the
provider, and one bulk-sender-compliance header set — and
missing any single layer tanks the inbox placement of a paid
plan's quota-warning email.

DNS automation is explicitly **deferred** (ADR-115 §D.5). The
record set is small and stable; operators add them by hand at
the DNS provider on day one. If the team ever grows a real
DNS-automation budget, this runbook becomes the test fixture.

---

## 1. Why this matters

- **Gmail + Yahoo bulk-sender rules (Feb 2024)** — both
  providers reject or junk mail from a domain with no
  DMARC record and an unaligned DKIM signature. Gregale's
  Resend / Postmark senders can sign the message correctly;
  if the *customer's* DNS is missing the alignment records
  the message never reaches the inbox.
- **List-Unsubscribe (RFC 8058)** — Gmail requires
  `List-Unsubscribe` + `List-Unsubscribe-Post: List-Unsubscribe=One-Click`
  on every bulk-sender message; missing either header = spam
  folder. The quota-warning template carries both headers
  when an unsubscribe URL is wired (issue #246 acceptance
  item 4). Dunning / billing / deletion mails deliberately
  do NOT carry the header — a customer one-click-unsubscribing
  from "your payment failed" would lose their suspension
  warning and get deleted silently. The split is in
  `pkg/mail/headers.go`.
- **From-domain alignment** — Resend rejects with HTTP 403
  if the From address's domain is not on the verified-domains
  list. The DNS records MUST land at the same domain as the
  From address, not at a parent.

## 2. The record set (Resend)

| Record | Type | Name | Value | Notes |
|--------|------|------|-------|-------|
| SPF    | TXT  | `@` (apex)   | `v=spf1 include:resend.com -all` | `-all` (hard-fail) — `-` is the spec, and a soft-fail leaks impersonation surface. Resend includes the `+include:resend.com` so any mail Resend sends is authorized. |
| DKIM   | TXT  | `resend._domainkey.<your-verified-domain>` | `p=...` value from Resend dashboard → Domains → `<your domain>` → DKIM | Resend rotates DKIM keys quarterly; the dashboard surfaces a "rotate now" button. Update this row when rotating. |
| DMARC  | TXT  | `_dmarc.<your-verified-domain>` | `v=DMARC1; p=quarantine; rua=mailto:dmarc-reports@<your-domain>; pct=100; adkim=s; aspf=s` | Start at `p=quarantine` for the first 30 days; flip to `p=reject` after the report stream shows clean alignment. `ruf=mailto:...` (forensic reports) is intentionally omitted — the volume is non-trivial and few providers render the reports well. |

Replace `<your-verified-domain>` with the From domain — typically
`gregale.dev` for the hosted platform or a customer-specific
domain for white-label deployments.

## 3. The record set (Postmark)

Postmark's selectors are different but the shape is identical:

| Record | Type | Name | Value |
|--------|------|------|-------|
| SPF    | TXT  | `@` (apex) | `v=spf1 include:spf.mtasv.net -all` |
| DKIM   | TXT  | `<selector>._domainkey.<your-verified-domain>` | `p=...` value from Postmark dashboard → Sender signatures → `<domain>` → DKIM |
| DMARC  | TXT  | `_dmarc.<your-verified-domain>` | same as Resend row above |

Postmark publishes the selector as part of the DKIM record in
the dashboard; the operator pastes verbatim.

## 4. Verifying the records

After adding the three rows, validate them BEFORE flipping the
transport to live:

```bash
# SPF
dig +short TXT gregale.dev | grep spf

# DKIM (replace <selector> with resend._domainkey or the
# Postmark equivalent)
dig +short TXT resend._domainkey.gregale.dev

# DMARC
dig +short TXT _dmarc.gregale.dev
```

Then a free third-party scan to catch the alignment + syntax
errors a hand-paste introduces:

- **Resend:** dashboard → Domains → "Verify"
- **Postmark:** dashboard → Sender signatures → "Verify"
- **mxtoolbox.com** / **mail-tester.com** — both run a
  deliverability pass that surfaces the alignment errors
  gmail / yahoo would.

## 5. Wiring the env on the box

`/etc/faas/sealed.env` carries the four `FAAS_MAIL_*` rows
(see `deploy/controlplane/sealed.env.example`). The ansible
control-plane role asserts at playbook time that
`FAAS_MAIL_TRANSPORT` is set to `resend` / `postmark` / `noop`
(or `log` + `FAAS_DEV=1`) so a misconfigured box fails the
playbook run, not the daemon boot.

Resend's webhook signing secret is the **separate** row
`FAAS_MAIL_RESEND_WEBHOOK_SECRET` — it does not enable
sending, it enables the bounce / complaint ingress at
`POST /v1/webhooks/resend`. The handler fails closed (503)
when the secret is empty, so a box that ships email but
forgets the webhook secret will not silently accept
unsigned bounce events.

## 6. Operator dry-run before flipping the transport

Run the dry-run on a staging box and eyeball the wire
payload — every template, every header — BEFORE flipping
production:

```bash
gregale mail dry-run --unsubscribe-url https://<your-domain>/u
```

Pipe through `jq` to assert the RFC 8058 pair is present:

```bash
gregale mail dry-run --unsubscribe-url https://<your-domain>/u | \
  jq '.[] | select(.name=="quota_warning") | .headers'
```

Expected output:

```json
{
  "List-Unsubscribe": "<https://<your-domain>/u>",
  "List-Unsubscribe-Post": "List-Unsubscribe=One-Click"
}
```

A typo here would let the dunning / billing templates pick up
the marketing header — covered by
`pkg/mail/dryrun_test.go::TestRenderAllTemplates_HeadersApplied`
+ `pkg/mail/renderer_transport_integration_test.go::TestRendererToTransport_DunningReachesWireWithoutUnsubscribe`,
both of which fail loudly on regression.

## 7. Day-2 monitoring

Three dashboards surface when the deliverability story drifts:

- **Prometheus** — `apid_mail_send_failures_total{reason}` (issue
  #246 item 2; reason enum: `bad_signature`, `rate_limited`,
  `unauthorized`, `server`, `unknown`). Sustained non-zero
  rate over 5 minutes = operator paging.
- **Prometheus, no transport** — `apid_mail_send_failures_total{reason="no_transport"}`
  counts every email dropped because `FAAS_MAIL_TRANSPORT=noop` on a
  production box (each drop is also a `mail.dropped` ERROR log record, and
  boot logs an ERROR `mail.transport transport=noop`). Any non-zero value
  means customers are not receiving address-verification, password-reset or
  magic-link mail: set `FAAS_MAIL_TRANSPORT=resend` (or `postmark`) plus
  `FAAS_MAIL_FROM` and the provider key in `/etc/faas/sealed.env`, then
  restart apid. Accounts that signed up meanwhile recover by signing in
  again: login mints and sends a fresh verification link while the address
  is unverified.
- **Audit log** — `webhook.replay_rejected` rows from the
  `webhookdedupe` table are normal (Resend retries every
  few seconds on a 5xx), but a 100x jump in
  `webhook.replay_rejected` count over 1 minute = the
  signing secret rolled and the apid env var didn't.
- **Resend dashboard** → Logs → filter by `bounce`. A
  bounce on a single recipient normally means a typo'd
  email at signup; a cluster of bounces across many
  accounts = SPF / DKIM alignment drifted (a DNS provider
  silently truncated the record).

## 8. Rotating the signing secret

Resend's "Roll" button in the dashboard → Webhooks
generates a new `whsec_…` value. The operator:

1. Pastes the new value into `/etc/faas/sealed.env` as
   `FAAS_MAIL_RESEND_WEBHOOK_SECRET=…`.
2. Restarts apid (`systemctl restart faas-apid`). The
   handler hot-reloads the secret at boot.
3. Does NOT click "Delete" on the old secret for 24
   hours — Resend continues delivering events signed
   with either key during the rotation window, and a
   too-fast rotation drops events.

A future ADR will cover webhook-deliveries-table-backed
replay dedupe (issue #294 follow-on); once that lands,
the rotation window can shrink to the 5-minute sync.Map
TTL because replays are no longer an unbounded set.

---

## See also

- ADR-115 (`docs/adr/115-transactional-email-provider-resend.md`)
  — the selection + boot-hardening decision this runbook
  implements.
- `pkg/mail/headers.go` — the RFC 8058 header pair +
  the policy table that excludes them from dunning /
  billing / deletion mails.
- `pkg/mail/dryrun.go` — `RenderAllTemplates` is the
  underlying helper behind `gregale mail dry-run`.
- `deploy/controlplane/sealed.env.example` — the
  operator-facing env-var contract.
- `deploy/ansible/roles/control_plane_service/tasks/main.yml`
  — the playbook-level fail-closed assert on
  `FAAS_MAIL_TRANSPORT`.
