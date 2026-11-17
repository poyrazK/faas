# Sub-processors

<!-- GENERATED — do not edit by hand; regenerate with `make subprocessor-md`. On-boarding checklist: subprocessor-onboarding.md. -->

Gregale uses the sub-processors below to provide the service. Each one
processes only the data listed in its row.

> **Notice window:** Processor shall notify Controller at least
> **30 days** before adding a new sub-processor. Controller may
> object on reasonable data-protection grounds; the parties shall
> work in good faith to resolve the objection before the change
> takes effect ([DPA](../DPA.md) §7).

## Current sub-processors

| Category | Vendor | Service | Data categories | Data region | DPA reference |
|---|---|---|---|---|---|
| website-hosting | Vercel | Hosting for the gregale.dev website, documentation and web front end | [website visitor request metadata (IP address, user agent, URL) in request logs] | Global edge network (US by default) | Vercel Data Processing Addendum (linked from the Vercel dashboard) |
| developer-platform | GitHub | GitHub App: repository access for deploys, Checks API and install-token exchange | [source archives of repositories the customer connects, fetched to build deployments, commit metadata (SHA, branch, author) for deploys and Checks, OAuth install-token exchange payloads] | US (GitHub default) | GitHub Data Protection Addendum (linked from GitHub Settings) |
| infrastructure-hosting | Google Cloud | Compute Engine virtual machines and Cloud Storage | [account and organization data (email, sign-in and security records, audit log), application source code, build artifacts and container images, application environment variables and secrets (secrets sealed with host keys before storage), application request logs and metrics, managed Postgres databases and object storage contents, backups of the above] | US (us-east1) | Google Cloud Data Processing Addendum (Cloud Data Processing Terms) |
| infrastructure-hosting | OVHcloud | Dedicated bare-metal servers for the control plane, compute nodes, managed Postgres and storage | [account and organization data (email, sign-in and security records, audit log), application source code, build artifacts and container images, application environment variables and secrets (secrets sealed with host keys before storage), application request logs and metrics, managed Postgres databases and object storage contents, backups of the above] | US (OVHcloud US data centres) | OVHcloud Data Processing Agreement (part of the OVHcloud contract terms) |
| network-dns-cdn | Cloudflare | DNS, reverse proxy and TLS termination, CDN and DDoS / web application firewall protection | [request metadata for traffic to Gregale and customer apps (IP address, headers, URL, timing), request and response bodies in transit through the proxy, DNS records for Gregale and customer custom domains] | Global (Cloudflare edge network) | Cloudflare Customer Data Processing Addendum (linked from the Cloudflare dashboard) |
| identity-provider | GitHub | GitHub OAuth | [GitHub user-id, primary verified email address, login handle, optional name and avatar URL] | US (GitHub default) | GitHub Data Protection Addendum (linked from GitHub Settings) |
| identity-provider | Google | Google OAuth (OpenID Connect) | [OAuth subject (sub), verified email address, optional profile name and avatar URL] | US (Google default) | Google Cloud Data Processing Addendum (linked from Google Cloud Console) |
| billing | Polar | Polar merchant of record + usage-based billing | [customer email, plan tier, metered usage (account-id + hours + GB-RAM-hours), invoice and subscription metadata] | Polar organization-configured region | Polar Data Processing Addendum (linked from Polar organization dashboard) |
| test-infrastructure | Amazon Web Services | Test and development environments | [test accounts and synthetic data only; no customer personal data in normal operation] | US | AWS Data Processing Addendum (part of the AWS Service Terms) |
| transactional-email | Resend | Resend transactional email transport + Svix-signed webhook (bounce / complaint / delivery events) | [recipient email (account holder only), subject + body of the transactional message, inbound bounce / complaint events: recipient email + bounce reason + provider event id] | US (Resend default) | Resend Data Processing Addendum (linked from Resend Dashboard) |
