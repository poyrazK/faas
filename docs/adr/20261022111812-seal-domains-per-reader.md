# ADR 20261022111812 · Seal domains: a sealed value opens only where it is read

- **Status:** accepted (2026-10-22). The first slice ships with this ADR:
  the registry, the gates, the `outbound` domain and the sealed GitHub
  webhook secret (see "Implemented in the first slice").
- **Date:** 2026-10-22
- **Amends (when accepted):** ADR-178 (fleet sealed-secret domain: one fleet
  identity becomes a set of domain identities), ADR-253 (its "dedicated
  outbound-only identity is future hardening" becomes the first domain),
  ADR-020 D1 (single host key), ADR-089 (the apid rekey runner stops
  re-sealing runtime secrets)
- **Related:** ADR-057 (host.age rotation), ADR-127 (sealed.env scope and its
  CI gate), ADR-1737 (internal mail's dedicated identities), ADR
  20261016151947 (livequeryd's dedicated keys identity),
  `docs/runbooks/breach-notification.md`

## Context

**The incident.** On 2026-10-21 Railway disclosed that an unauthorized party
had reached the systems that store customer environment variables and
obtained decrypted contents for some of them
([disclosure](https://blog.railway.com/p/security-disclosure-wednesday-october-21st),
[status timeline](https://status.railway.com/incident/QH8L8B3E)). Railway
linked it to a campaign against platforms that hold AI inference keys. It
told every customer to rotate every secret, invalidated all API tokens and
OAuth sessions, rotated every bucket credential, cut GitHub integration
tokens and paused builds for about a day. Encryption at rest did not help:
the attacker reached something that could decrypt.

**The same shape here.** Gregale seals customer secrets with age
(`pkg/secretbox`). Since ADR-178, new ciphertext is sealed to one identity,
`/etc/faas/secrets/fleet.age`. That identity opens:

- every customer runtime secret (`app_secrets`, `project_environment_secrets`,
  the release secret snapshots and bundles, sidecar env);
- customer AI provider and other outbound keys
  (`outbound_integration_credentials`, `outbound_integration_credential_keys`,
  ADR-253 and ADR-991);
- private registry passwords (`app_registry_credentials`,
  `job_registry_credentials`);
- GitHub install tokens, GitLab OAuth tokens, Slack and Linear tokens, MCP
  catalog OAuth tokens;
- MFA TOTP secrets, inbound and outbound webhook signing secrets, alert
  channel targets, PagerDuty keys, log drain credentials, object storage
  secret keys, managed Postgres credentials and the cluster signing key.

The audit for this ADR found:

1. **Eleven units hold the private fleet identity**: apid, the apid clone
   worker, the apid data-pipeline worker, schedd, gatewayd-internal,
   githubd, meterd, outboundd, imaged and s3-gatewayd through
   `LoadCredential=` (`pkg/daemonunitspec/*.go`), and vmmd, which runs as
   root and reads the directory directly (`secretbox.LoadFleetAndHostKeys`,
   `cmd/vmmd/main.go`).
2. **Every node gets it**, whatever its role: `deploy/ansible/node_join.yml`
   stages `fleet.age` before bootstrap. Compute nodes run untrusted guest
   code. A guest escape to root on any compute node therefore yields every
   customer's AI provider keys and GitHub tokens, although nothing on a
   compute node reads them.
3. **apid holds it for MFA.** `pkg/apid/apidserver/main.go` loads it to open
   TOTP secrets. In request paths apid only seals runtime secrets, but two
   places use the private key without needing it:
   - the opt-in rekey runner (`FAAS_REKEY_ENABLED`,
     `pkg/apid/apidserver/rekey_runner.go`) opens every `app_secrets` row
     inside apid;
   - `rotateAppSecret` reads `identities[0]` only to stamp the `kid`, which
     is a property of the public recipient.
4. **Env names hide it.** githubd, imaged, meterd and s3-gatewayd receive the
   fleet identity as `FAAS_HOST_AGE_IDENTITY_PATH=%d/faas_fleet_age_identity`.
   `pkg/daemonunitspec/envcontract.go` lists only apid and outboundd as owners
   of `FAAS_FLEET_AGE_IDENTITY_PATH`, so a search for the fleet name
   undercounts its holders.
5. **Namespaces are not a boundary.** `SealBytes` writes an authenticated
   namespace tag, and most openers check it. That stops one envelope being
   replayed as another; it does not stop a holder of the key from reading
   any envelope.
6. **One secret is not sealed at all.** `github_webhook_secrets.secret_value`
   holds each installation's GitHub webhook HMAC secret as raw bytes
   (migration 00212). A database read lets an attacker forge webhook
   deliveries for every installation.
7. **The pattern this ADR generalizes already exists.** Internal mail uses
   dedicated identities (`internal-mail/mail.age` for maild,
   `internal-mail/producer-verification.age` for apid's outbox, ADR-1737).
   livequeryd opens its keys with `livequeryd/keys.age`. The off-host
   control-plane state backup is sealed only to offline operator recipients
   (`deploy/ansible/roles/cp_state_backup`). gatewayd-public's production units
   set no `FAAS_HOST_KEY_PATH` (only a Lima development manifest does), so in
   production it holds no identity.

Appendix A lists all 93 production decrypt call sites outside
`pkg/secretbox`, by reader.

## Decision

### 1. A sealed value is sealed to the domain of the service that reads it

A **seal domain** is an age X25519 identity named by purpose. Each domain has
a fixed list of reader units. A unit holds the private identities of its own
domains and nothing else. Writers hold public recipients only, so apid keeps
sealing customer secrets but can no longer open them.

| domain | identity | readers | node role | opens |
|---|---|---|---|---|
| `runtime` | `domains/runtime.age` | vmmd | compute | `app_secrets`, `project_environment_secrets`, `app_config_guard_secrets`, `managed_postgres_cutover_credentials`, `deployment_release_secret_snapshots`, `project_release_secret_bundles`, sidecar env |
| `outbound` | `domains/outbound.age` | outboundd | control plane | `outbound_integration_credentials`, `outbound_integration_credential_keys` |
| `build` | `domains/build.age` | imaged | compute | `app_registry_credentials`, `job_registry_credentials` |
| `source` | `domains/source.age` | githubd | control plane | `github_installations.sealed_install_token`, `github_webhook_secrets` (sealed by this ADR, see 5) |
| `edge` | `domains/edge.age` | gatewayd-internal | compute | `apps.public_auth_basic`, `app_log_drains`, `application_standard_log_destinations`, rehearsal object credentials |
| `alerting` | `domains/alerting.age` | meterd | control plane | `alert_rules`, `notification_channels`, `mcp_readiness_pagerduty_policies`, `mcp_readiness_fallback_policies` |
| `storage` | `domains/storage.age` | s3-gatewayd | where s3-gatewayd runs | `object_storage_s3_credentials` |
| `delivery` | `domains/delivery.age` | schedd | both | `execution_payloads`, `agent_execution_workflows.sealed_plan`, `app_webhooks`, trigger config envelopes, `commit_sources` |
| `control` | `domains/control.age` | schedd; `gregalectl` at node join | both | `cluster_signing_keys`, internal service keys (`internal_svc`, `cluster_svc`) |
| `account` | `domains/account.age` | apid, apid clone worker, apid data-pipeline worker | control plane | `accounts.mfa_secret_encrypted`, `inbound_webhook_endpoints`, GitLab, Slack, Linear and MCP OAuth rows, `managed_realtime_*`, `project_environment_clone_*`, `data_pipeline_*`, `product_control_email_*`, `account_billing_connections`, `mcp_readiness_pagerduty_acknowledgements` |

Internal mail and livequeryd keep their existing dedicated identities and
join the registry as the `mail`, `mail-verification` and `livequery` domains.

Identities live under `/etc/faas/secrets/domains/` (mode 0400 root:root) and
are delivered to units with `LoadCredential=`, as ADR-127 requires. Each
domain keeps the `.previous` overlap and retirement of ADR-057.

### 2. Nodes receive only their role's domains

Node join stages the identities of the units that run on the node's role
(`pkg/daemonunitspec/registry.go`). A compute node holds `runtime`, `build`,
`edge`, `delivery` and `control`. A control-plane node holds `outbound`,
`source`, `alerting`, `account`, `delivery`, `control` and `storage` if
s3-gatewayd runs there. A compute node never holds the keys to customer AI
provider credentials, GitHub tokens or account material.

### 3. Reads that cross a domain move to the domain's reader

| today | after |
|---|---|
| apid rekey runner opens every `app_secrets` row | the runtime re-seal runs as the operator one-shot of section 6; apid's runner keeps only the `account` domain |
| `rotateAppSecret` derives the `kid` from the private identity | derived from the domain recipient |
| apid opens alert rule webhook secrets and notification channel targets for preset and test sends (`handlers_alert_presets.go`, `handlers_notification_channel_test_send.go`) | apid records a test-send request; meterd opens the target and sends it |
| apid opens GitLab OAuth tokens (`environment_gitlab_source.go`) | stays in `account` for now; moving source fetches behind githubd is a follow-up |
| the apid clone worker opens managed Postgres checkpoint material and seals a `DATABASE_URL` into `app_secrets` | unchanged: it opens `account` and seals to the `runtime` recipient |

### 4. A registry and three ratchet gates keep it that way

A new package, `pkg/sealdomain`, is the single table of domains, their reader
units and identity paths, their roles, and the sealed columns each one owns.
One program, `scripts/ci/sealdomaincheck` (`make seal-domain-scope-check`, in
`make lint`, `make lint-changed`, CI's lint job and ci-light), runs three
checks against `scripts/ci/sealdomaincheck/baseline.txt`. A finding missing
from the baseline fails, and so does a baseline line that no longer occurs,
so the file stays exact and only shrinks:

1. **Credentials.** Every `LoadCredential=` of a private identity (a path
   under `/etc/faas/secrets` ending in `.age` or `.age.previous`) in a unit
   file or an Ansible drop-in. A unit may load the identities of the domains
   it reads, and a `seal_domains` reader drop-in its domain's identity;
   today's fleet and host holders are the baseline.
2. **Loaders.** Direct calls to `secretbox.LoadHostKey`, `LoadHostKeys`,
   `LoadFleetAndHostKeys`, `age.ParseX25519Identity` and
   `age.ParseIdentities` in daemon code, counted per file. New code loads
   through `sealdomain.LoadReaderIdentities`; the operator CLIs and the
   defining packages are exempt. The credential check cannot see vmmd
   reading the directory as root; this one can.
3. **Columns.** Every `bytea` column in `schema.sql` is registered to a
   domain, is a probe or a known plaintext secret, or is listed as not
   sealed (hashes, digests, signatures, payload bodies). Names are not a
   reliable signal: `apps.public_auth_basic` is sealed and has no "seal" in
   its name. A new sealed column must name its domain.

### 5. Seal `github_webhook_secrets` into `source`

githubd already caches each secret for 60 seconds
(`pkg/githubd/webhook_secret.go`), so one unseal per installation per minute
keeps the webhook path cheap. Expand with a nullable `secret_sealed` column
(`SET LOCAL lock_timeout = '5s';`), dual-write, backfill, switch reads, and
drop `secret_value` in a later release.

### 6. Migration: expand, dual-seal, re-seal, contract, one domain at a time

Release N runs beside N-1 (`docs/release-compatibility.md`). A reader on N-1
cannot open an envelope sealed only to a domain it has never heard of, and a
rollback would bring such readers back. Every step stays openable by the
previous release.

1. **Expand.** `gregalectl seal-domain init --domain D` generates the
   identity into the join artifact set (the ADR-178 transport). Ansible
   stages it by role. A new `seal_domain_probe(domain, recipient,
   sealed_blob, updated_at)` table holds one non-secret probe per domain,
   sealed through `secretbox`. The singleton `fleet_seal_domain_probe` stays
   until the last contract. Readers load `[D current, D previous, fleet,
   fleet previous, host, host previous]`. Node join (`pkg/fleetseal.Verify`)
   opens the probe of every domain staged for the role before it activates
   the node. A `domain` label on the readiness proof
   (`faas_seal_domain_ready{domain}`, next to `faas_fleet_seal_domain_ready`)
   feeds an alert in the style of `FaasFleetSealDomainUnverified`.
2. **Dual-seal.** A per-domain switch, `FAAS_SEAL_DOMAIN_<D>_WRITES`
   (`fleet`, `dual`, `domain`; empty means `dual` once the domain recipient
   is staged), makes writers seal to both the domain and the fleet
   recipients. age encrypts to several recipients natively;
   `secretbox.SealBytesTo` wraps it. Readers on N and N-1 both open these
   envelopes, so a rollback is safe. The switch and the recipient are unit
   configuration rendered by the `seal_domains` role, not runtime
   configuration in PostgreSQL: a database writer must not be able to point
   sealing back at the fleet key or at a recipient of its own. The role
   stages the reader's identity and runs `gregalectl seal-domain verify`
   before it gives the writer the recipient.
3. **Re-seal.** `gregalectl seal-domain migrate --domain D` walks D's
   registered columns. It opens each envelope with the fleet identity and
   writes it back sealed to the domain recipient alone, using
   compare-and-swap. This extends `gregalectl fleet-seal migrate` and pkg/rekey's
   CAS documents. It runs as a short-lived operator command on a
   control-plane node and never inside a daemon. Progress is the count of
   envelopes in D's columns that still open with the fleet identity, plus
   the `kid` on tables that have one (`app_secrets.kid`). The switch moves
   to `domain` once N-1 has left the rollback window.
4. **Contract.** When D's count is zero, D's readers drop the fleet and host
   `LoadCredential=` lines and the baseline entries go with them. Once every
   domain has contracted, node join stops staging `fleet.age`. The fleet
   identity is then kept only offline, sealed to the cp-state operator
   recipients, so PostgreSQL backups taken before migration can be restored.
   It is destroyed when the oldest such backup expires.

**Order.** `outbound` first: AI provider keys are what this campaign wants,
it has one reader on one role, and ADR-253 already anticipated it. Then
`runtime`, the largest and the one Railway lost. Then `build`, `source`,
`edge`, `alerting`, `storage`, `delivery` and `control`. `account` comes last.
By then the fleet identity apid still holds opens only `account` rows.

After the first slice, each domain arrives on its own: wire the writer and
reader, re-seal, then contract in a later release.

## Implemented in the first slice

This ADR ships with:

- `pkg/sealdomain`: the registry, `LoadReaderIdentities`, write modes and
  `Recipients`, domain probes, and the webhook-secret seal; and
  `pkg/sealdomain/sealmigrate` for the re-seal and the backfill.
- `secretbox.SealBytesTo` (several recipients).
- Migration `20261022090211159_seal_domains`: `seal_domain_probe`, and
  `github_webhook_secrets.secret_sealed` with a `NOT VALID` CHECK.
- The `outbound` domain, wired: apid seals customer outbound credentials to
  `FAAS_SEAL_DOMAIN_OUTBOUND_RECIPIENT_PATH` under
  `FAAS_SEAL_DOMAIN_OUTBOUND_WRITES`; outboundd opens them with
  `FAAS_SEAL_DOMAIN_OUTBOUND_IDENTITY_PATH` and keeps the fleet identity
  (and now its `.previous` twin, which it used to ignore); the
  `seal_domains` Ansible role stages the files, renders both drop-ins, and
  verifies the probe in between. Commit connections, which shared apid's
  outbound recipient, stay on the fleet recipient: schedd reads them.
- GitHub webhook secrets: the admin route writes a sealed copy beside the
  plaintext column; githubd prefers the copy and fails closed when it does
  not open; `gregalectl seal-domain migrate --domain source` backfills the
  copy for older rows. Until `source` is wired the copy is sealed to the
  fleet recipient, which still takes the secret out of a plain database
  read.
- `gregalectl seal-domain init|verify|migrate`, with
  `faas_seal_domain_ready{domain}` written by `verify`.
- The `kid` of app and project secrets is the seal recipient, so sealing
  needs no private identity.
- `scripts/ci/sealdomaincheck` and its baseline.

Rollout checklist: [Seal domains rollout](../runbooks/seal-domains-rollout.md).

## Implemented in the second slice: the runtime domain

- **vmmd** loads `domains/runtime.age` (and its `.previous`) next to
  `host.age` when it is staged, ahead of the fleet and host identities. It
  reads the file directly as root, so there is no credential to add. It
  exports `faas_seal_domain_loaded{domain="runtime"}` from its start.
- **apid** seals every runtime secret through `runtimeSecretRecipient()`:
  - app and project secrets (set and rotate);
  - the clone worker's `DATABASE_URL`;
  - object storage binding values (a new `RuntimeSecretRecipient` on the
    object host);
  - project release secret bundles;
  - managed PostgreSQL credential delivery;
  - sidecar env.

  The recipient comes from `FAAS_SEAL_DOMAIN_RUNTIME_RECIPIENT_PATH` and
  `FAAS_SEAL_DOMAIN_RUNTIME_WRITES`, delivered by an apid drop-in from the
  `seal_domains` role.
- **Runtime goes straight from `fleet` to `domain`, with no `dual` mode.**
  apid refuses `dual` for runtime. Its writers span several packages that
  each take one recipient, and dual sealing would protect nothing while the
  fleet identity still opens every row. The switch to `domain` waits until
  every vmmd reports the identity loaded and the previous release has left
  the rollback window.
- **No rows go back to the fleet key.** In domain mode, apid's rekey runner
  no longer re-seals runtime secrets (it would undo the domain, and apid
  cannot open domain rows). `gregalectl fleet-seal migrate` skips rows whose
  `kid` names a recipient it holds no identity for.
- **Re-seal:** `gregalectl seal-domain migrate --domain runtime
  --readers-verified` re-seals every stored runtime secret through the
  release-secret rekey documents (ADR-1139): app and project environment
  secrets, staged release bundles, deployment snapshots, and release and
  promotion captures. Each document is written back with a compare-and-swap
  that changes only `kid` and ciphertext, so no runtime-change trigger fires
  and value fingerprints and versions stay as they were. The store lists only
  documents with an envelope labelled for another key, so a complete
  migration lists nothing.
- **Alerts:** `FaasSealDomainUnverified` (`faas_seal_domain_ready == 0`)
  and `FaasSealDomainIdentityMissing` (a reader without the identity while
  another reader of the domain holds it); runbook `FaasSealDomain`.
- **Staging:** the `seal_domains` role stages the identity on compute and
  single-box hosts and verifies the runtime probe there. It stages the
  recipient and the apid drop-in on control-plane hosts. bootstrap runs the
  role ahead of `vmmd_service` on compute nodes.

## Implemented in the third slice: the source domain

- **githubd** loads `domains/source.age` (and its `.previous`) through
  `FAAS_SEAL_DOMAIN_SOURCE_IDENTITY_PATH`, a `LoadCredential` drop-in from
  the `seal_domains` role. It opens install tokens and webhook secret copies
  with it ahead of the fleet and host identities, and exports
  `faas_seal_domain_loaded{domain="source"}`.
- **Writers follow `FAAS_SEAL_DOMAIN_SOURCE_WRITES`** (empty, `fleet`,
  `dual` or `domain`, as for outbound):
  - githubd seals each install token it mints or refreshes;
  - apid seals webhook secret copies to `FAAS_SEAL_DOMAIN_SOURCE_RECIPIENT_PATH`
    and holds only that public recipient.
- **Install tokens are not migrated.** A GitHub installation token expires
  within the hour, and githubd seals its successor under the write mode, so
  the column moves to the domain on its own.
- **Re-seal:** `gregalectl seal-domain migrate --domain source
  --readers-verified` moves every webhook secret copy to the domain:
  - a copy a legacy identity opens is re-sealed from what it opens to;
  - a row without a copy gets one;
  - a copy only the domain opens is current.

  Writes are compare-and-swap on the stored copy. Without
  `--readers-verified`, the command still backfills missing copies to the
  fleet recipient.
- **Staging:** the role stages the identity, the recipient and both
  drop-ins on control-plane and single-box hosts. It restarts githubd and
  proves the probe before apid seals anything to the domain.
- `secretbox.SealTo` seals an env-map envelope to several recipients, as
  `SealBytesTo` does for byte blobs. `sealdomain.RegisterLoaded` is the
  shared `faas_seal_domain_loaded` gauge.

The webhook secret's plaintext column `secret_value` still exists, so
sealing its copy protects little until that column is gone. Retiring it
takes two releases:

1. Migration `20261023201337575_github_webhook_secret_plaintext_optional`
   makes the column nullable and adds a `NOT VALID` check that a row keeps
   at least one copy. apid stops writing the plaintext, and stores only the
   sealed copy. githubd has preferred the sealed copy since the release that
   added it, which is already the previous release, and still falls back to
   the plaintext for older rows.
2. The next release drops `secret_value`. Before it ships, every
   environment must have run `gregalectl seal-domain migrate --domain
   source`, so that no row depends on the plaintext.
The install tokens are the gain now: they grant read access to customer
repositories, and once writes are domain-only, within the hour the fleet
identity opens none that is still valid.

## Implemented in the fourth slice: the build domain

- **imaged** loads `domains/build.age` (and its `.previous`) through
  `FAAS_SEAL_DOMAIN_BUILD_IDENTITY_PATH`, a `LoadCredential` drop-in from
  the `seal_domains` role on compute and single-box hosts. It exports
  `faas_seal_domain_loaded{domain="build"}`.
- **imaged keeps two identity lists.** App and job private-registry
  passwords open with a registry-only list: the build domain first, then
  the fleet and host identities. Crash captures, a `runtime` column imaged
  still reads (section 3), keep the fleet and host identities and never see
  the build identity.
- **apid seals both registry password writers** (app and job credentials)
  to `FAAS_SEAL_DOMAIN_BUILD_RECIPIENT_PATH` under
  `FAAS_SEAL_DOMAIN_BUILD_WRITES` (empty, `fleet`, `dual` or `domain`, as for
  outbound). It holds only the public recipient.
- **Re-seal:** `gregalectl seal-domain migrate --domain build
  --readers-verified` re-seals both tables with compare-and-swap writes. It
  shares one byte-envelope re-seal with the outbound migration. Like the
  runtime one, it runs on a compute host, the only place `build.age` lives.
- **Staging:** the role stages the identity, restarts imaged and proves the
  probe on compute hosts. It stages the recipient and apid's drop-in on
  control-plane hosts. Until writes are `domain`, apid also seals to the
  fleet key, so the two converges can run in either order.

## Implemented in the fifth slice: the edge domain

- **gatewayd-internal** loads `domains/edge.age` (and its `.previous`)
  through `FAAS_SEAL_DOMAIN_EDGE_IDENTITY_PATH`, a `LoadCredential` drop-in
  from the `seal_domains` role on compute and single-box hosts. It exports
  `faas_seal_domain_loaded{domain="edge"}`.
- **One identity list** opens app basic-auth credentials and log drain
  headers: the edge domain first, then the fleet and host identities. The
  log drain manager no longer loads keys itself; it takes that list.
- **apid seals both writers** to `FAAS_SEAL_DOMAIN_EDGE_RECIPIENT_PATH` under
  `FAAS_SEAL_DOMAIN_EDGE_WRITES` (empty, `fleet`, `dual` or `domain`):
  - the basic-auth credential set by `PATCH /v1/apps/{slug}`;
  - log drain headers, for customer drains and application-standard log
    destinations alike.
- **gatewayd-public is not a reader.** The DNS-provider token it opens is
  not stored in an edge column, so the registry lists gatewayd-internal
  alone, and on compute only.
- **Re-seal:** `gregalectl seal-domain migrate --domain edge
  --readers-verified` re-seals basic-auth credentials and customer log drain
  headers with compare-and-swap writes, on a compute host. A row a guard
  holds for now (a project clone in progress, a pending standard review)
  is counted as changed and left for the next run.
- **Not re-sealed yet: application standards.** The migration counts these
  as `skipped` and leaves them on the fleet key:
  - `application_standard_log_destinations` rows are immutable versions;
  - a drain a standard manages must carry its destination's exact
    ciphertext, and its delivery binding hashes that ciphertext;
  - the control backups a standard keeps hold drain headers in jsonb.

  New versions are sealed to the domain under the write mode. Re-sealing
  the existing ones needs the standards to allow a re-seal that keeps their
  hashes, and is a later slice. Until then gatewayd-internal keeps the fleet
  identity, which the contract step removes only after every edge row
  opens with the domain.
- **Rehearsal object credentials** (behind `FAAS_REHEARSAL_OBJECT_FIXTURES`,
  stored in a jsonb document) stay on the fleet key in this slice.

## Implemented in the sixth slice: the alerting domain

- **meterd** loads `domains/alerting.age` (and its `.previous`) through
  `FAAS_SEAL_DOMAIN_ALERTING_IDENTITY_PATH`, a `LoadCredential` drop-in from
  the `seal_domains` role on control-plane and single-box hosts. A
  configured identity that does not load fails startup. meterd exports
  `faas_seal_domain_loaded{domain="alerting"}`.
- **One identity list** opens every alerting value: the alerting domain
  first, then the fleet and host identities. It covers alert rule webhooks,
  notification channels (alerts and financial budgets), MCP readiness
  deliveries and relayed test sends.
- **apid seals the six writers** to `FAAS_SEAL_DOMAIN_ALERTING_RECIPIENT_PATH`
  under `FAAS_SEAL_DOMAIN_ALERTING_WRITES`:
  - an alert rule's webhook secret (create, update, rotate, preset enable);
  - a notification channel's destination;
  - the MCP readiness PagerDuty and fallback keys.

  The MCP PagerDuty acknowledgement signing secret is an `account` column
  and stays as it is. Tests apid cannot open go through the relay below.
- **Re-seal:** `gregalectl seal-domain migrate --domain alerting
  --readers-verified` re-seals the five columns with compare-and-swap
  writes on a control-plane host. Each value keeps its namespace, including
  the legacy `alert_rule` one the evaluator still opens. Open MCP readiness
  incidents keep copies of their route's key in `delivery_routing` (jsonb);
  those are not re-sealed and age out with the incidents.

## Implemented in the seventh slice: the storage domain

- **s3-gatewayd** loads `domains/storage.age` (and its `.previous`) through
  `FAAS_SEAL_DOMAIN_STORAGE_IDENTITY_PATH` on control-plane hosts, opens S3
  credentials with it ahead of the fleet and host identities, and exports
  `faas_seal_domain_loaded{domain="storage"}`.
- **The startup rekey stays out of the domain.** s3-gatewayd re-seals every
  credential whose `kid` is not its current key when it starts. Two rules
  keep that from undoing the domain or moving rows into it ungated:
  - it skips any credential the storage identity opens;
  - apid files a credential under the fleet recipient's key id while the
    credential still opens with the fleet key (write modes empty, `fleet`
    and `dual`), and under the domain's only in `domain` mode
    (`sealdomain.KeyID`).

  A gateway without the domain still refuses to start over a credential it
  cannot open, so `readers_verified` must cover every s3-gatewayd and its
  previous release.
- **apid seals all eight writers** to `FAAS_SEAL_DOMAIN_STORAGE_RECIPIENT_PATH`
  under `FAAS_SEAL_DOMAIN_STORAGE_WRITES`: customer credentials, compute
  bindings and their rotation, signed URLs, and the project environment
  clone, shared binding and clone checkpoint credentials.
- **Re-seal:** `gregalectl seal-domain migrate --domain storage
  --readers-verified` re-seals every active credential, signed URLs
  included, to the domain alone under the domain's key id. Its
  compare-and-swap is on the ciphertext, not the key id, because a binding
  rotation replaces a secret in place. Revoked credentials and expired
  signed URLs are left as they are. The binding promotion revision bumps,
  as for outbound.

## Implemented in the eighth slice: the control domain

- **schedd** loads `domains/control.age` (and its `.previous`) through
  `FAAS_SEAL_DOMAIN_CONTROL_IDENTITY_PATH` on control-plane and compute
  hosts. It opens the cluster signing key with the control identity first,
  then fleet and host, and keeps working with the control identity alone
  once the contract step removes `fleet.age`. It exports
  `faas_seal_domain_loaded{domain="control"}`, and a configured identity
  that does not load fails startup.
- **No writer and no write mode.** apid never writes the key. `gregalectl
  fleet-seal migrate` creates it, sealed to fleet, only on a new fleet; it
  moves into the domain only through `gregalectl seal-domain migrate
  --domain control --readers-verified`, a compare-and-swap on the single
  row.
- **fleet-seal follows the domain.**
  - `fleet-seal migrate` leaves a cluster key the control identity opens
    where it is, instead of re-sealing it to fleet.
  - `fleet-seal verify`, which node join runs before activation, opens it
    with the control identity when the fleet key no longer does, and
    reports `cluster_key_sealed_to`.
  - Both read the staged `control.age` (`--control-key` overrides). The
    join stages it first: `seal_domains` runs in `bootstrap.yml`, ahead of
    the activation play.
- **CD** stages the control identity on both node roles; it has no
  recipient to stage, since nothing seals to it but the migration.

## Implemented: the alerting test-send relay

Section 3 moves apid's alerting test sends behind meterd, ahead of the
`alerting` domain itself.

- **apid records a request** in `alert_test_sends` when it cannot open a
  notification channel's destination or an alert rule's webhook secret. The
  request names the channel or rule, plus the test event for a webhook.
- **meterd** claims requests every second (`FOR UPDATE SKIP LOCKED` with a
  two-minute lease). It opens the destination with its own identities,
  sends the test as apid would have, and writes the result.
- **apid waits** for the result and answers as before, then deletes the
  request. The wait is 45 seconds for a channel and two minutes for a
  webhook, whose test retries like a real delivery. On timeout apid answers
  504. meterd prunes requests older than an hour.
- **While apid can open a value it still sends the test itself.** No request
  exists until a value is sealed to the domain alone, and the domain's
  `readers_verified` gate already requires every meterd to run a release
  with the relay. So a meterd one release older never sees a request. apid
  stops opening alerting values when it loses the fleet identity, in the
  contract step.

## CD and node join stage the domain keys

Each domain's key pair is a pair of GitHub environment secrets:
`SEAL_DOMAIN_<DOMAIN>_AGE_KEY` and `SEAL_DOMAIN_<DOMAIN>_AGE_RECIPIENT`.

- `scripts/ci/seal_domain_vars.sh` writes the files a host role needs on the
  runner and points the `seal_domains` role at them:
  - control plane: the `outbound`, `source`, `alerting`, `storage` and
    `control` identities and every recipient;
  - compute: the `runtime`, `build`, `edge` and `control` identities.
- The control-plane converges (the full one and the host-configuration
  lane) and the compute join run the role with those variables.
- A join whose `seal-domain verify` fails leaves the node drained.
- Each domain's public recipient is written as a comment, so a rotated key
  changes the converge contract hash.
- Write modes and `readers_verified` stay in the environment's Ansible
  variables. CD refuses a variables file that sets the key variables itself.
- `gregalectl seal-domain verify --recipient-only` checks that a staged
  recipient is the one readers published the probe with. The role runs it
  before apid's writer drop-in, so a recipient from another pair stops the
  converge instead of making apid seal to a key no reader holds.

Later slices:
- the other domains, in the order above;
- re-sealing the short-lived copies in `app_config_guard_secrets` and
  `managed_postgres_cutover_credentials`, which the rekey documents do not
  cover;
- the contract steps;
- meterd sending test alerts;
- dropping `github_webhook_secrets.secret_value` (step 2 above), after the
  source backfill has run everywhere.

## Consequences

**Blast radius, after every domain has contracted:**

| compromise | today | after |
|---|---|---|
| code execution in apid (internet-facing) | every sealed column | `account` columns |
| code execution in gatewayd-internal or imaged (compute; imaged handles untrusted images) | every sealed column | `edge` or `build` columns |
| root on a compute node (for example a guest escape) | every sealed column | `runtime`, `build`, `edge`, `delivery`, `control` |
| root on a control-plane node | every sealed column | the control-plane domains; runtime secrets stay closed |
| PostgreSQL dump alone | ciphertext, plus GitHub webhook secrets in clear | ciphertext only |

- **Incident response gets a scope.** A compromised unit or node role tells
  operators which domains to rotate and which customers to notify. Today the
  only safe answer is Railway's: everyone, everything.
- **More keys to operate.** About ten identities, each with init, overlap and
  retirement. They all run through `gregalectl seal-domain`, the per-domain
  probes and the one registry. Rotating one domain no longer touches the
  others.
- **The benefit arrives per domain, at contract.** During `dual` the fleet
  identity still opens new rows. The rollout takes several releases.
- **Residual exposure is explicit.** apid still opens account material (MFA
  secrets, integration OAuth tokens, inbound webhook secrets). A root
  attacker on a node reads every domain staged for that role. Every compute
  node can open every customer's runtime secrets, not only those of apps
  placed on it. Parked snapshots still hold runtime secrets in clear (ADR-020
  D5; the `ephemeral` secret class opts out).

## Rejected alternatives

- **Keep one key and harden apid.** This leaves nine other holders,
  including root on every compute node.
- **External KMS or HSM.** A compromised reader can still ask the KMS to
  decrypt, so domains are needed either way. It would also put a network
  dependency on the wake path and does not fit bare-metal nodes.
- **Per-account or per-app keys.** They put key material in PostgreSQL
  (ADR-020 D1), and every reader would still need all of them.
- **Re-wrap runtime secrets per node at placement.** This would let a node
  open only the secrets of apps placed on it, but it needs a broker on the
  wake path and its own latency budget. It is the natural next step once
  `runtime` has contracted, and it needs its own ADR.
- **Rely on namespace checks.** They already exist (Context, finding 5).
  They authenticate an envelope's purpose; they do not restrict who can read
  it.

## Not covered here

Unseal telemetry and honeytoken secrets, platform-wide break-glass controls
(build pause, mass token and session invalidation), and platform-held
provider keys in operator env files (ADR-249) need their own decisions.

## Appendix A · Production decrypt call sites (2026-10-22, main ec54635eca)

Found with `grep -rn 'secretbox\.\(Open\|OpenMulti\|OpenBytes\|OpenBytesMulti\)('`
over `cmd/` and `pkg/`, excluding tests and `pkg/secretbox`. Direct
`age.Decrypt` calls outside secretbox serve operator backup and archive
restore (`cmd/gregalectl/commands_backup*.go`, `pkg/cpstate`,
`pkg/managedpostgres/copyarchive/archive.go`) and are out of scope.

| reader unit | call sites | opens | target domain |
|---|---|---|---|
| vmmd | `pkg/fcvm/manager.go:3354, 3387, 3439` | runtime secrets, sidecar env | `runtime` |
| outboundd | `pkg/outbound/sealed_credentials.go:70, 125` | outbound and AI provider credentials | `outbound` |
| imaged | `pkg/imaged/handler.go:2193`, `pkg/imaged/job_registry_auth.go:37` | registry passwords | `build` |
| githubd | `pkg/githubd/realservice.go:712`, `cmd/githubd/source_fetcher.go:171` | install tokens | `source` |
| gatewayd-internal | `cmd/gatewayd-internal/public_auth_unsealer.go:103`, `log_drains.go:771`, `rehearsal_objects.go:34` | basic auth, log drain auth, rehearsal object credentials | `edge` |
| gatewayd-public | `cmd/gatewayd-public/main.go:363` | DNS provider token (unconfigured in production units) | `edge` |
| meterd | `pkg/alerts/evaluator.go:658`, `channels.go:153`, `mcp_readiness_pagerduty.go:69` | alert webhook secrets, channel targets, PagerDuty keys | `alerting` |
| s3-gatewayd | `cmd/s3-gatewayd/main.go:134`, `pkg/s3gateway/rekey.go:45` | S3 secret keys | `storage` |
| livequeryd | `cmd/livequeryd/discover.go:93`, `pkg/livequery/liveserver/keys.go:94` | live keys and discovered logins (already `livequeryd/keys.age`) | `livequery` |
| schedd | `pkg/webhook/dispatcher.go:682`, `pkg/triggerconfig/config.go:202`, `pkg/commit/credentials.go:57`, `pkg/executionpayload/payload.go:162, 209, 274` | webhook signing secrets, trigger configs, commit connections, execution payloads and plans | `delivery` |
| schedd | `cmd/schedd/cluster_key_loader.go:211`, `internal_svc_minter.go:314` | cluster signing key, internal service key | `control` |
| apid | `pkg/apid/apidserver/handlers_mfa.go:665, 836`, `pkg/apid/mailoutbox/mfa_enabled_transition.go:85` | MFA TOTP | `account` |
| apid | `pkg/apid/apidserver/handlers_inbound_webhooks.go:394, 403` | inbound webhook signing secrets | `account` |
| apid | `pkg/apid/mcpcatalog/*` (7 sites), `pkg/apid/mcpreadiness/monitor.go:193`, `pagerduty_webhook.go:74` | MCP OAuth tokens and clients, PagerDuty webhook secrets | `account` |
| apid | `pkg/apid/issueslack/*` (3), `pkg/apid/issuetrackers/*` (4), `pkg/apid/apidserver/environment_gitlab_source.go:45` | Slack, Linear and GitLab tokens | `account` |
| apid | `pkg/apid/managedrealtime/handlers.go:289`, `handlers_push.go:44` | realtime credentials, push provider keys | `account` |
| apid | `pkg/apid/apidserver/managed_postgres_credentials.go:83`, `project_environment_clone_checkpoint_*.go` (2), `pkg/managedpostgres/*` (10) | managed Postgres credentials and clone material | `account` |
| apid | `pkg/datapipeline/*` (2), `pkg/productemail/crypto.go:20`, `pkg/apid/apidserver/statement_invoices_host.go:52` | pipeline connections, product email grants, billing connections | `account` |
| apid | `pkg/apid/apidserver/handlers_alert_presets.go:539`, `handlers_notification_channel_test_send.go:67` | alert targets for test sends | move to `alerting` (section 3) |
| apid | `pkg/rekey/rekey.go:338`, `release_snapshots.go:86` | every runtime secret, during rekey | move to the operator one-shot (section 3) |
| apid (dedicated identity) | `pkg/apid/mailoutbox/*` (12 sites) | mail producer outbox | `mail-verification` (unchanged) |
| maild (dedicated identity) | `pkg/internalmail/codec.go:42`, `keyring_codec.go:160` | mail payloads, DKIM keys | `mail` (unchanged) |
| gregalectl (operator) | `pkg/fleetseal/fleetseal.go` (6) | migration and join verification | extended by section 6 |
