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
| website-hosting | Vercel | Hosting for the gregale.dev website, documentation and web front end | [website visitor request metadata (IP address, user agent, URL) in request logs] | Global edge network (US by default) | Vercel Data Processing Addendum (linked from the Vercel dashboard) | `—` |
| developer-platform | GitHub | GitHub App: repository access for deploys, Checks API and install-token exchange (outbound from githubd) | [source archives of repositories the customer connects, fetched to build deployments, commit metadata (SHA, branch, author) for deploys and Checks, OAuth install-token exchange payloads] | US (GitHub default) | GitHub Data Protection Addendum (linked from GitHub Settings) | `—` |
| infrastructure-hosting | Google Cloud | Compute Engine virtual machines and Cloud Storage | [account and organization data (email, sign-in and security records, audit log), application source code, build artifacts and container images, application environment variables and secrets (secrets sealed with host keys before storage), application request logs and metrics, managed Postgres databases and object storage contents, backups of the above] | US (us-east1) | Google Cloud Data Processing Addendum (Cloud Data Processing Terms) | `—` |
| infrastructure-hosting | OVHcloud | Dedicated bare-metal servers for the control plane, compute nodes, managed Postgres and storage | [account and organization data (email, sign-in and security records, audit log), application source code, build artifacts and container images, application environment variables and secrets (secrets sealed with host keys before storage), application request logs and metrics, managed Postgres databases and object storage contents, backups of the above] | US (OVHcloud US data centres) | OVHcloud Data Processing Agreement (part of the OVHcloud contract terms) | `—` |
| network-dns-cdn | Cloudflare | DNS, reverse proxy and TLS termination, CDN and DDoS / web application firewall protection | [request metadata for traffic to Gregale and customer apps (IP address, headers, URL, timing), request and response bodies in transit through the proxy, DNS records for Gregale and customer custom domains] | Global (Cloudflare edge network) | Cloudflare Customer Data Processing Addendum (linked from the Cloudflare dashboard) | `—` |
| identity-provider | GitHub | GitHub OAuth | [GitHub user-id, primary verified email address, login handle, optional name and avatar URL] | US (GitHub default) | GitHub Data Protection Addendum (linked from GitHub Settings) | `—` |
| identity-provider | Google | Google OAuth (OpenID Connect) | [OAuth subject (sub), verified email address, optional profile name and avatar URL] | US (Google default) | Google Cloud Data Processing Addendum (linked from Google Cloud Console) | `—` |
| billing | Polar | Polar merchant of record + usage-based billing | [customer email, plan tier, metered usage (account-id + hours + GB-RAM-hours), invoice and subscription metadata] | Polar organization-configured region | Polar Data Processing Addendum (linked from Polar organization dashboard) | `FAAS_BILLING_PROVIDER` |
| test-infrastructure | Amazon Web Services | Test and development environments | [test accounts and synthetic data only; no customer personal data in normal operation] | US | AWS Data Processing Addendum (part of the AWS Service Terms) | `—` |
| transactional-email | Resend | Resend transactional email transport + Svix-signed webhook (bounce / complaint / delivery events) | [recipient email (account holder only), subject + body of the transactional message, inbound bounce / complaint events: recipient email + bounce reason + provider event id] | US (Resend default) | Resend Data Processing Addendum (linked from Resend Dashboard) | `FAAS_MAIL_TRANSPORT` |

## Sub-processor on-boarding checklist

When adding a new sub-processor:

1. **Update `subprocessors.json`** — append a new object with all required fields (`id`, `category`, `vendor`, `service`, `data_categories`, `data_region`, `encryption`, `retention_days`, `dpa_signed`, `dpa_reference`, `operator_switch_env`, `rationale`, `notice_published_at`, `effective_date`). The `id` must be a stable kebab-case slug; never reuse a removed entry's id (use `subprocessor-archive.json`).
2. **Set `notice_published_at`** — the date the operator first publishes the 30-day notice at `https://gregale.dev/docs/dpa/subprocessors`. This timestamp must be **≥ 30 days older** than the planned `effective_date`. The `subprocessor-check` CI gate fails the build if this invariant is violated.
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
