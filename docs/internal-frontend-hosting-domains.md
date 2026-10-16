# Internal custom domains and managed HTTPS

This default-off extension of the static hosting pilot is described in
[ADR-202610160959](adr/202610160959-internal-hosting-custom-domains.md).
Standard-plan recovery is amended by [ADR-202610161106](adr/202610161106-standard-plan-hosting-domain-recovery.md).
It requires durable sites. All domain operations remain operator-only and
account-owned. Nothing here enables customer entitlements, public site visits,
builds, or runtime containers.

## Prepare the dedicated pilot

1. Complete the existing [hosting setup and qualification](internal-frontend-hosting.md).
   Keep a dedicated, separate hosting zone; never share the SaaS Worker zone
   with platform, API, or dashboard traffic.
2. Enable Cloudflare for SaaS on that zone through the operator's existing
   provider workflow. Standard plans are supported: Gregale does not send
   Enterprise custom metadata. Cloudflare's normal SaaS, certificate, Worker,
   storage, and Access pricing still applies; check the account's allowances.
   No subscription or billing configuration is changed by this code.
   Read the operator-assisted recovery procedure below before enabling admission.
3. The custom hostname's DNS must be outside the SaaS provider's Cloudflare
   account. Same-account custom hostnames do not receive Access protection
   under Cloudflare's documented model. Use an external DNS provider or a
   different account and a **DNS-only CNAME**, without proxying or flattening.
4. Review/apply Terraform with `attach_route = true`,
   `durable_routing_enabled = true`, and `configure_custom_domains = true`.
   Keep `custom_domains_enabled = false` while preparing the infrastructure.
   This changes the dedicated zone's Worker pattern to `*/*`, adds proxied
   `customers.<hosting-domain>` pointing at reserved non-serving `192.0.2.1`,
   and configures that hostname as the SaaS fallback. Main serving can remain
   disabled during preparation. No request should reach a Gregale server.
5. Deploy every apid writer with the new migration before admitting domains.
   Keep all returned bootstrap flags at `0` initially. Do not roll back old
   apid writers while domain rows exist.

Use the Terraform `apid_environment` output and inject a separate sealed
`FAAS_FRONTEND_HOSTING_DOMAINS_CF_TOKEN`. It needs Custom Hostnames edit/read on
the dedicated SaaS zone, Zone read for its isolation check, and Access Apps and
Policies read/edit on its account.
Keep provisioning credentials, the domain token, and the KV token separate.
Apid never manages DNS records in customer zones or modifies the shared
internal Access policy.

| Setting | Purpose |
|---|---|
| `FAAS_FRONTEND_HOSTING_DOMAINS_ENABLED` | Bootstrap the independent default-off management flag |
| `FAAS_FRONTEND_HOSTING_DOMAINS_ZONE_ID` | Dedicated SaaS zone |
| `FAAS_FRONTEND_HOSTING_DOMAINS_ACCESS_POLICY_ID` | Terraform-owned reusable internal allow policy |
| `FAAS_FRONTEND_HOSTING_DOMAINS_CNAME_TARGET` | Exact `customers.<hosting-domain>` target |
| `FAAS_FRONTEND_HOSTING_DOMAINS_CF_TOKEN` | Separate scoped secret; never stored in API/SQL output |
| `FAAS_FRONTEND_HOSTING_RESERVED_DOMAINS` | Additional platform registrable domains, comma-separated |

Changing provider account, zone, target, or Access policy pins a different
provider identity. Existing rows refuse provider writes/removal against a
replacement configuration. Restore the original configuration to finish cleanup.

## Internal dashboard and API

Build the operations console with both
`NEXT_PUBLIC_FRONTEND_HOSTING_ENABLED=true` and
`NEXT_PUBLIC_FRONTEND_HOSTING_DOMAINS_ENABLED=true`. Point it at this pilot API.
After preparation, opt into Worker custom serving with
`custom_domains_enabled = true`. Enable `frontend_hosting_enabled` and
`frontend_hosting_domains_enabled` through
Runtime Configuration only after providers are configured. New durable sites
also require the existing durable creation flag. The capability response
advertises the domain controls, CNAME target, and enforced limits.

In site management, add a hostname such as `portal.customer.example`. The
pilot permits five domain rows per site, including removing rows, and 64 ASCII
bytes per normalized hostname. Unicode names normalize to IDNA before the
global uniqueness check. The 64-byte limit uses the provider's standard
certificate hostname mode; branded long-name certificates are not requested.
Platform/hosting roots, apex names, public suffixes, IPs, ports, paths, and
wildcards are rejected.

Add the displayed records at the domain's DNS provider:

- TXT `_gregale-hosting.<hostname>` = `gregale-hosting=<claim challenge>`.
- DNS-only CNAME `<hostname>` = the pilot's configured target.
- Cloudflare hostname/certificate validation records once they appear.

Gregale does no provider write until its own TXT and CNAME are both observed.
The controller then creates the custom hostname once and durably binds its returned ID, requests a
DNS-TXT managed certificate, and creates a private Access application with
the internal policy. Keep validation records for certificate renewal.

The dashboard distinguishes DNS pending, certificate preparation, HTTPS ready,
and domain removal. Routing publication is separate: a ready certificate does
not mean every edge location has seen the alias. Metadata caches may lag up to
60 seconds. Site visits require that domain's own Access sign-in; tokens for
the generated site URL are not accepted on custom domains. Provider status is
the last observation, not a continuous end-to-end health check.

REST routes below the existing `/v1/internal/hosting/sites/{site}` root:

| Method and suffix | Request |
|---|---|
| `GET /domains` | List account-owned domains and DNS/certificate/publication status |
| `POST /domains` | `{"hostname":"portal.customer.example"}` with an idempotency key |
| `POST /domains/{domain}/recover` | Explicit provider UUID, hostname, and reviewed revision |
| `DELETE /domains/{domain}` | `{"expected_revision":1}` with an idempotency key |

Every route independently enforces flags, operator identity, MFA, scope, and
ownership. Changing the management flag also blocks replayed successful writes.
On an uncertain response, refresh before another intent. The server does not
silently adopt foreign Cloudflare hostnames or Access apps.

## Retry, remove, and recover

Pending work retries every minute. Ten processing leases per pass and two
workers bound provider/SQL work; a pass has a 30-second dispatch budget. Active
domains are checked every 30 minutes. TLS renewal itself is managed by
Cloudflare. Known validation/issuance/deployment timeouts and expired requests
receive a bounded validation refresh, at most once every 30 minutes; pending
records remain visible for DNS repair. No provider API or SQL call is on the
static request path.

Closed error codes are `dns_pending`, `provider_unavailable`,
`provider_conflict`, `publication_unavailable`, and `origin_unavailable`.
Provider outages retain the last published alias. A hostname-create attempt is
recorded durably before provider I/O. If the response or SQL receipt is lost,
Gregale does not issue another create, adopt a hostname search result, or purge
uncertain cleanup state. It reports a conflict requiring operator recovery.
Even a crash before the request was sent conservatively requires this recovery.
Raw provider errors and credentials are not stored or returned.

### Recover an uncertain hostname create

1. Wait at least 160 seconds from `details.create_started_at` (processing lease
   plus provider and dispatch budgets). Refresh the domain to review its current
   hostname and revision. The server enforces this delay.
2. Inspect the dedicated zone's custom hostnames in Cloudflare. Find the exact
   resource belonging to this attempt, using provider activity/audit evidence.
   A matching hostname alone is not ownership evidence. If no resource exists
   after the request has settled, create a disposable hostname manually with
   exactly this hostname, SSL type `dv`, method `txt`, and no custom origin.
   Do not create another resource while a prior provider request is unresolved.
3. Copy the exact provider hostname UUID. Use **Bind reviewed provider ID** in
   the operations panel, including when the domain is already removing. The
   confirmation explicitly authorizes Gregale to manage and delete that resource.
   Alternatively POST `/domains/{domain}/recover` under the site root with
   `provider_id`, `expected_hostname`, and `expected_revision` plus an idempotency key.
4. The API reads that exact provider ID, verifies zone/account, hostname,
   certificate mode, absence of custom origins, and any legacy ownership markers.
   A revision-fenced write binds the ID, increments the revision, invalidates old
   leases, and audits the operator action. It does not enable routing or cancel
   deletion. Provisioning or terminal cleanup resumes on the next pass.

Only an unknown provider ID can be bound; this endpoint cannot replace an
existing binding or reset the create attempt. Conflicts require refresh and
operator review, without automatic retries. A known provider ID that disappears
is not automatically recreated. Legacy Enterprise objects retain marker checks;
new resources use persisted provider IDs without metadata. Access application
lost-response recovery still uses its unique claim-specific application name.

Removal first commits deleting intent and publishes a permanent disabled R2
alias. After the current generation's receipt and 150-second grace, the
controller removes owned Cloudflare hostname and Access resources and purges
the domain row. Keep DNS pointed at the pilot until cleanup completes, then
remove the customer records. Provider outages retain cleanup state. Site
deletion marks all domains deleting atomically and cannot purge the site while
domain rows remain. Promotions, rollbacks, suspension, and release retirement
are enforced through the canonical site head without per-domain copies.

Hostname reservations and `hosting/domains/` terminal heads remain after
cleanup. Do not add lifecycle expiration for this prefix or delete reservations
to reassign a hostname. Pilot hostname reassignment needs a separate reviewed
transfer protocol. Do not recreate the schema against an existing pilot bucket.

Disabling domain **management** stops new requests but preserves safety
reconciliation. Disabling the Worker's **custom-domain** switch rejects custom
hosts before cache lookup; the generated site hostname still follows its main
and durable switches. Terraform currently owns the SaaS route and fallback
under `configure_custom_domains`: drain/qualify domains before removing that
infrastructure. The independent main Worker switch remains the emergency stop.

## Qualification

Portable checks cover memory/PostgreSQL ownership and concurrent quotas,
global reservations, lease recovery, stale-result fencing, cleanup receipts and
grace; single-attempt create fencing, explicit operator recovery, and refusal of foreign legacy markers; Worker
audience checks, cache denial, canonical promotion/suspension/retirement, and
warm/cold serving without API renewal. Mocked Terraform tests prove default-off
resources, route isolation, and independent admission. The dashboard's unit and
local browser checks use fixtures, not Cloudflare.

Before live internal use, record evidence from a disposable external domain:

1. Confirm the SaaS fallback is active, wildcard TLS works on the generated
   hostname, and standard-plan custom hostname creation works without custom metadata.
2. A wrong/missing Gregale TXT or CNAME must leave DNS pending with no provider
   resources. Correct records should produce the additional provider challenges.
3. Wait for actual certificate issuance and active hostname status, then confirm
   the Access login flow and exact internal policy. Test an external identity,
   a forged JWT, and a valid generated-host token on the custom hostname; all
   must be denied, including warm files, HEAD, and conditional requests.
4. Exercise deploy, rollback, suspension/resume, and release retirement through
   the custom hostname. Capture bytes and routing generations across US/EU
   probes; record actual edge observations rather than declaring global success
   from a publication receipt.
5. Exercise API/database outages and domain-provider/R2 publication failures.
   Warm and cold files must survive control-plane loss; new publication and
   removal must retry safely after recovery.
6. Remove the domain while cached content exists. Verify terminal publication
   and grace before provider deletion, eventual hostname/Access cleanup, retained
   SQL reservation and R2 tombstone, canonical-host continuity, and refusal to
   claim the removed hostname from another site.
7. Qualify certificate renewal with retained validation DNS and the provider's
   observed renewal behavior. Issuance alone is not renewal evidence.

Live qualification requires dedicated pilot credentials and domain bindings.
The portable checks do not provision cloud resources, change DNS, request
certificates, or enable runtime flags.
