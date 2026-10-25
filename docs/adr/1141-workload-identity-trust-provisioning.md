# ADR-1141 · Workload-identity trust provisioning

- **Status:** accepted
- **Date:** 2026-10-14
- **Context:** vmmd mints workload-identity tokens for app instances over the
  guest loopback endpoint, and apid verifies them for Flags, Operations and,
  since ADR-1140, queue leases; outboundd and cloud providers trust the same
  issuer. Nothing in `deploy/` installs either half: vmmd's
  `workload_identity_key_path` and apid's `flags_workload_jwks_path` are
  operator TOML that no role sets, so a fleet built from Ansible answers every
  token request with `identity_not_configured`. A worker using the ADR-1140 SDK
  consumer then gets 401 on every lease and the failure is visible nowhere.
  docs/workload-identity.md already fixes the trust model: one RSA key shared
  by every vmmd, published as one JWKS at the issuer, because AWS, GCP and
  Cloudflare federation each trust a single JWKS.
- **Decision:**
  1. *Key material.* `gregalectl workload-identity init --out DIR` writes a
     3072-bit RSA key (`workload-identity.key`, 0400), its key id
     (`workload-identity.kid`, 0444; the RFC 7638 thumbprint, so every key
     has a distinct reproducible id) and the public JWKS
     (`workload-identity.jwks.json`, 0444). `rotate` writes a new key and id
     and keeps the current public key in the JWKS so tokens it signed verify
     until they expire; `status` checks the three files agree. The tool
     validates the JWKS with apid's verifier before writing anything.
  2. *Distribution.* The Ansible variable `faas_workload_identity_source_dir`
     names DIR on the controller. `vmmd_service` installs the key and key id
     under `/etc/faas/secrets` (root 0400 / 0444) and renders
     `99-faas-workload-identity.conf` with `FAAS_WORKLOAD_IDENTITY_KEY_PATH`
     and `FAAS_WORKLOAD_IDENTITY_KEY_ID`; `control_plane_service` installs the
     JWKS at `/etc/faas/workload-identity.jwks.json` (public, readable by the
     `faas-apid` user without a credential) and renders
     `99-faas-workload-trust.conf` with `FAAS_WORKLOAD_JWKS_PATH`. With the
     variable empty, files an operator placed by hand are used the same way;
     with no files the drop-ins are removed and issuance stays off. Both
     roles assert ownership and modes, and the control plane asserts the JWKS
     holds only public keys. Roll out the control plane first so apid trusts
     a new key before any vmmd signs with it.
  3. *apid trust.* A cluster-wide `workload_jwks_path` / `workload_issuer`
     (env `FAAS_WORKLOAD_JWKS_PATH` / `FAAS_WORKLOAD_ISSUER`) backs queue
     leases, and Flags and Operations fall back to it when their own
     settings are empty. Precedence: surface env, surface TOML, cluster env,
     cluster TOML; the existing Flags and Operations settings keep working.
  4. *Visibility.* vmmd counts token requests in
     `vmmd_workload_identity_tokens_total{result}`;
     `FaasWorkloadIdentityNotConfigured` warns when apps ask a node that has
     no key.
- **Consequences:** A fleet converged with the source directory set mints
  tokens on every compute node and apid accepts them, so SDK queue consumers,
  Flags and Operations reporting work without an API key in the image. The
  private key exists on the operator workstation and the compute nodes only.
  Cloud federation still needs the JWKS published at the issuer URL; that is
  unchanged operator work. outboundd keeps its own TOML setting and can point
  at the same JWKS path.
- **Rejected alternatives:** Per-node keys published to Postgres (the
  ADR-053 node-key pattern) give each node its own key, but cloud providers
  trust one issuer JWKS that would have to be re-published on every node
  change. Sealing the key in Postgres with `fleet.age` (the ADR-125 cluster
  signing-key pattern) needs vmmd to hold the fleet identity and a new
  unseal path for one static file. Assembling the JWKS from each node's
  `/.well-known/jwks.json` in Ansible couples the control-plane play to every
  compute node being reachable.
- **Validation:** `gregalectl` tests cover init, refusal to overwrite, rotate
  keeping the previous key, status, and a token minted with the written key
  verifying against the written JWKS. apid tests cover the trust precedence
  and the queue-lease workload path through the cluster verifier. The JWKS
  assertions were exercised against public, private-member and empty key
  sets. The env contract, alert metadata and emitted-metric checks pass.
- **Rollback:** Clear `faas_workload_identity_source_dir` and delete the
  files; the next converge removes both drop-ins and issuance returns to
  `identity_not_configured`.

## Amendment 2026-10-17 — tokens for booting instances

vmmd minted a token only for an instance in its live map, which an instance
enters when its boot returns. A guest's workload starts well before that: a
worker is RUNNING only after guest-init has watched it for the whole
`api.CharacterizationDeadline` (10 s). On n2 (`TestWorkerScaleFromZeroMetal`)
a worker pool scaling from zero had its consumer process up about 2 s after
the send, then refused every token request for about 10 s; the first lease
came at a median 11.9 s, and 10 s of that was the window.

vmmd now mints for an instance that is live or still booting
(`fcvm.Manager.WorkloadTokenIdentity`). A wake fixes the app and account when
it takes the lease, before the guest starts, and the token carries the same
claims it would carry once the instance is live. Qualification restores record
no identity and still get none. apid already accepts a `gregale:queues` token
for an instance in `waking` or `cold_booting`, and refuses it once the
instance leaves the live states, so a consumer whose boot fails cannot lease
with the token it took. A token for another audience stays valid for its
five-minute TTL either way; the code that asked for it is the app's own,
booting or not.

Only the token endpoint changes. The outbound relay (ADR-989) and in-guest
event publishing still resolve live instances only.

Measured on n2 with the same test after the change: the first lease came at a
median 2.0 s (1.9–2.2 s over five cycles), about 1.7 s after the boot
started, while RUNNING still came at 11.9 s. The test now fails if any worker
takes its token after it is RUNNING. A burst still boots workers one at a
time (first leases at 2.0, 13.9 and 25.6 s for three workers), because the
pool admits the next worker only when the previous boot returns.

## Amendment 2026-10-25 — tokens name their environment

A security review found that every instance of an app received the subject
`app:<app_id>`, whatever deployment scope (environment) it ran. Scopes are the
`--env` of a deploy: a staging deployment of an app received exactly the
token a production deployment did, so a cloud role whose trust policy names
`app:<app_id>` accepted code that was only ever reviewed for staging. AWS IAM
can condition a generic OIDC provider's trust on `sub` and `aud` alone, so a
separate claim would not let customers tell the two apart there.

- Guest tokens carry an `environment` claim: the scope of the deployment the
  instance runs.
- The subject stays `app:<app_id>` in the `default` scope and becomes
  `app:<app_id>:env:<scope>` in any other. A trust policy written for the
  default scope keeps working; one for a named scope names it.
- vmmd reads the scope from the instance's deployment. When it cannot (no
  deployment recorded, or the read fails), it refuses the request with
  `environment_unavailable` rather than mint the default subject. A node with
  no database keeps minting without the claim.
- apid's verifier and outboundd accept either form when it agrees with the
  token's `environment` claim. They ship with the control plane, before
  vmmd mints the new form (docs/release-compatibility.md order).
- vmmd's host-internal relay tokens (ADR-989) keep the plain subject.

**Customer impact:** an app whose production runs in a named scope, rather
than `default`, must update its trust policies to
`app:<app_id>:env:<scope>`. Gregale is pre-GA, and the docs only ever showed
the default scope.
