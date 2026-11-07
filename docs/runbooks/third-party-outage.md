# Third-party service outage

Use this when a service we depend on but do not run is down or degraded:
Cloudflare, GitHub, the email providers, Polar, or the ACME certificate
authority. If the provider may have been **compromised** rather than just
being down, also follow [breach-notification.md](breach-notification.md).

First confirm it is them and not us: check the provider's status page, then
reproduce from a workstation outside the fleet. Record the provider incident
URL in the incident record.

| Provider | What breaks | What keeps working | Section |
|---|---|---|---|
| Cloudflare | Proxied app and API traffic, DNS answers, WAF, Workers (`get.gregale.dev`) | Running apps, in-fleet traffic, direct-entry hosts with nothing in front | §1 |
| GitHub | Merges, CI, releases, automatic deploys, the deploy-freeze label, CLI installs from GitHub | Running platform, customer apps, existing release artifacts already on hosts | §2 |
| Email (Resend, Postmark) | Sign-in links, notifications, alert email (Pushover still pages) | Everything else | §3 |
| Polar | Checkout, plan changes, billing webhooks | Running apps, metering (it queues) | §4 |
| ACME CA | New and renewing certificates for custom domains | Existing certificates until they expire | §5 |

Severity: SEV1 if customers cannot reach their apps (usually only Cloudflare);
otherwise SEV2 or SEV3 by the matrix in
[incident-response.md](incident-response.md).

## 1. Cloudflare

1. **Scope.** Is it DNS resolution, the proxy, or one product (WAF, Workers,
   tunnel)? `dig +short <app host> @1.1.1.1` and `@8.8.8.8`, then `curl -v`
   the same host. A resolution failure affects every name in the zone.
2. **Proxy only, DNS fine.** Hosts with nothing in front keep serving. For a
   long outage the owner may move selected hosts to direct entry, following
   "Nothing in front" in [EdgeDirectEntry.md](EdgeDirectEntry.md). This
   removes Cloudflare's flood protection, so watch
   [FaasEdgeAttackMode.md](FaasEdgeAttackMode.md) signals and move back
   ("Under attack: back behind Cloudflare") once Cloudflare recovers.
3. **DNS down.** Nothing in the fleet fixes this. Records cached by
   resolvers keep working until their TTL runs out (60 s for edge records),
   so impact grows quickly. Moving name servers is a registrar change that
   takes hours to propagate. Do it only for a multi-hour outage, and only
   if a secondary DNS provider has been prepared in advance
   ([dns-registrar-compromise.md](dns-registrar-compromise.md), "Before you need it").
4. **Workers.** `get.gregale.dev` installs fail. Tell users to install from
   the GitHub release assets directly.
5. Post status updates that name the provider and link its status page.

## 2. GitHub

1. **Freeze is implied.** Automatic deploys cannot run. Nothing to do
   unless you need a repair deploy.
2. **Repair deploy without GitHub.** There is none. Repair and rollback
   deploys are both workflow dispatches
   ([DeployFreeze.md](DeployFreeze.md), "Deploy during a freeze"), and hosts
   only accept signed releases. Use containment that needs no new code:
   drain a node (`gregalectl compute-nodes drain`), flip a platform flag, or
   `gregalectl break-glass pause`. Customer app rollbacks through the API
   still work.
3. When GitHub returns, check for queued deploys before lifting anything:
   several merges may roll out at once. Open a `deploy-freeze` issue first if
   you want to release them one at a time.

## 3. Email providers

1. Pages still arrive through Pushover. Warn-severity alerts, which are
   email only, do not. Watch Grafana directly until mail returns.
2. Sign-in by email link fails. Users with other sign-in methods (OAuth,
   API keys, agent keys) are unaffected. Say so in the status update.
3. Do not switch providers mid-incident unless both are down. The sending
   domain's SPF, DKIM and DMARC records must already authorize the second
   provider, or the mail will be rejected anyway.

## 4. Polar

Follow [PolarBillingWebhooks.md](PolarBillingWebhooks.md). Polar retries
webhooks, but it disables an endpoint after ten consecutive failures. After a
long outage on either side, check that the endpoint is still enabled in Polar. Do not hand-edit plans or
entitlements while it is down. Dunning and suspension must not act on
payment state that could not be refreshed: check
[FaasDunningDeletionBlocked.md](FaasDunningDeletionBlocked.md) before
resuming.

## 5. ACME certificate authority

Existing certificates keep working. Renewals start 30 days before expiry, so
a CA outage only matters if it lasts longer than the renewal window. Watch
[FaasTLSCertExpiryWarn.md](FaasTLSCertExpiryWarn.md) and
[FaasCustomDomainTLS.md](FaasCustomDomainTLS.md). New custom domains stay
pending until the CA returns. Do not raise retry rates: that turns an outage
into a rate-limit lockout.

## Close

Record which provider failed, for how long, and what we could not do. If the
answer was "nothing, we waited", decide in the post-mortem whether that is
acceptable or needs a second provider.
