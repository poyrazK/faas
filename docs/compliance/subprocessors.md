# Sub-processors

<!-- GENERATED — do not edit by hand; regenerate with `make subprocessor-md`. -->

Single source of truth: [`docs/compliance/subprocessors.json`](subprocessors.json).

> **Notice window:** Processor shall notify Controller at least
> **30 days** before adding a new sub-processor. Controller may
> object on reasonable data-protection grounds; the parties shall
> work in good faith to resolve the objection before the change
> takes effect (docs/DPA.md §7). The 30-day window is enforced by the
> `subprocessor-check` CI gate (PR-3): every new sub-processor
> entry must carry a `notice_published_at` timestamp that is at
> least 30 days older than `effective_date` before the operator
> can deploy the change.

## Current sub-processors

| Category | Vendor | Service | Data categories | Data region | DPA reference | Operator switch |
|---|---|---|---|---|---|---|
| developer-platform | GitHub | GitHub Checks API + install-token exchange (outbound from githubd) | [SHA-only commit metadata (Checks API), OAuth install-token exchange payloads] | US (GitHub default) | GitHub Data Protection Addendum (linked from GitHub Settings) | `—` |
| identity-provider | GitHub | GitHub OAuth | [GitHub user-id, primary verified email address, login handle, optional name and avatar URL] | US (GitHub default) | GitHub Data Protection Addendum (linked from GitHub Settings) | `—` |
| identity-provider | Google | Google OAuth (OpenID Connect) | [OAuth subject (sub), verified email address, optional profile name and avatar URL] | US (Google default) | Google Cloud Data Processing Addendum (linked from Google Cloud Console) | `—` |
| off-host-backup | Hetzner | Hetzner Storage Box (SFTP + rclone) | [encrypted Postgres dump (AES-256 / X25519-sealed via ADR-020)] | EU (FSN1 / NBG1 datacenters) | docs/DPA.md §7 | `—` |
| billing | Paddle | Paddle Billing (explicit compatibility provider) | [customer email, plan tier, metered usage (account-id + hours + MB-RAM-hours)] | EU or US (Paddle entity selected by the merchant) | Paddle Data Processing Addendum (linked from Paddle Dashboard) | `FAAS_BILLING_PROVIDER` |
| billing | Polar | Polar merchant of record + usage-based billing | [customer email, plan tier, metered usage (account-id + hours + GB-RAM-hours), invoice and subscription metadata] | Polar organization-configured region | Polar Data Processing Addendum (linked from Polar organization dashboard) | `FAAS_BILLING_PROVIDER` |
| database | Hetzner | Hetzner managed single-tenant Postgres | [account metadata, app metadata, deployment artifacts (digests only — secrets never stored here), audit events, usage metering records] | EU (FSN1 / NBG1 datacenters) | docs/DPA.md §7 | `—` |
| billing | Stripe | Stripe Billing + metered usage records (legacy opt-in) | [customer email, plan tier, metered usage (account-id + hours + MB-RAM-hours), invoice items] | US (Stripe default) — controller can opt into EU via Stripe account settings | Stripe Data Processing Addendum (linked from Stripe Dashboard) | `FAAS_BILLING_PROVIDER` |
| transactional-email | Postmark | Postmark transactional email transport + bounce / spam-complaint webhook | [recipient email (account holder only), subject + body of the transactional message, inbound bounce / spam-complaint events: recipient email + bounce reason + message id] | US (Postmark default) | Postmark Data Processing Addendum (linked from Postmark Dashboard) | `FAAS_MAIL_TRANSPORT` |
| transactional-email | Resend | Resend transactional email transport + Svix-signed webhook (bounce / complaint / delivery events) | [recipient email (account holder only), subject + body of the transactional message, inbound bounce / complaint events: recipient email + bounce reason + provider event id] | US (Resend default) | Resend Data Processing Addendum (linked from Resend Dashboard) | `FAAS_MAIL_TRANSPORT` |

## Sub-processor on-boarding checklist

When adding a new sub-processor:

1. **Update `subprocessors.json`** — append a new object with all required fields (`id`, `category`, `vendor`, `service`, `data_categories`, `data_region`, `encryption`, `retention_days`, `dpa_signed`, `dpa_reference`, `operator_switch_env`, `rationale`, `notice_published_at`, `effective_date`). The `id` must be a stable kebab-case slug; never reuse a removed entry's id (use `subprocessor-archive.json`).
2. **Set `notice_published_at`** — the date the operator first publishes the 30-day notice at `https://docs.gregale.dev/dpa/subprocessors`. This timestamp must be **≥ 30 days older** than the planned `effective_date`. The `subprocessor-check` CI gate fails the build if this invariant is violated.
3. **Regenerate `subprocessors.md`** — run `make subprocessor-md`. Hand-edits to the markdown file are caught at `git diff --exit-code docs/compliance/subprocessors.md` time (same pattern as `spec-check`).
4. **Update DPA §7** — add a bullet to `docs/DPA.md` §7 listing the new sub-processor. The DPA is the executed contract; the JSON is the rendering source for the public notice.
5. **Update vendor assessment** — if the new sub-processor is critical-tier (database, billing, identity-provider), write a one-file assessment under `docs/compliance/vendor-assessments/` (PR-10).
6. **Update sub-processor archive** — never delete an entry from `subprocessors.json`. Mark it for removal (set `effective_until` + `removal_reason`), keep the entry visible until `effective_until` elapses, then move it to `subprocessor-archive.json`.

## Removed sub-processors

See [`subprocessor-archive.json`](subprocessor-archive.json).

## Cross-references

- `docs/DPA.md` §7 (executed DPA — the contract).
- `docs/faas_implementation_spec.md` §11 (security hardening checklist).
- `docs/compliance/soc2-control-mapping.md` CC6.6 / CC9.1 / CC9.2.
- `docs/compliance/iso27001-statement-of-applicability.md` A.5.19 / A.5.20 / A.5.21 / A.5.22.
- `cmd/subprocessor-md` (CI gate generator).
- `pkg/netns` and `pkg/oci` (network egress enforcement — see `cmd/denylist-md` for the network-side mirror of this PR).
