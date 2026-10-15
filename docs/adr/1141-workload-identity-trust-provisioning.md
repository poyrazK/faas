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
