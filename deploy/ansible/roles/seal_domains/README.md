# seal_domains

Stages per-reader seal-domain identities (ADR 20261022111812). This release
wires one domain, `outbound`: customer AI provider and other outbound
credentials, opened only by outboundd.

1. On the operator machine: `gregalectl seal-domain init --domain outbound --dir <dir>`.
2. Set `faas_seal_domains_managed: true`,
   `faas_seal_domain_outbound_identity_src: <dir>/outbound.age` and
   `faas_seal_domain_outbound_recipient_src: <dir>/outbound.age.pub`, then
   converge the control plane. The role gives outboundd the identity,
   proves it opens the domain probe (publishing the probe on the first
   node), and only then has apid seal new credentials to the domain and the
   fleet recipient.
3. Once every outboundd verified the domain and the previous release left
   the rollback window, set `faas_seal_domain_outbound_readers_verified: true`
   and `faas_seal_domain_outbound_writes: domain`, converge, and run
   `gregalectl seal-domain migrate --domain outbound --readers-verified`.
   Re-sealing a credential bumps the binding promotion revision of the apps
   bound to it, so pending promotion approvals for them must be renewed.
4. The contract step (removing the fleet identity from outboundd) ships in a
   later release.
