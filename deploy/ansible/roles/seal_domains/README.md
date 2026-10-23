# seal_domains

Stages per-reader seal-domain identities (ADR 20261022111812). Four domains
are wired, each opened by one daemon:
- `outbound`: customer AI provider and other outbound credentials (outboundd);
- `runtime`: customer env vars and secrets (vmmd);
- `source`: GitHub install tokens and webhook secrets (githubd);
- `build`: app and job private-registry passwords (imaged).

The `outbound` steps:

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

The `runtime` domain covers app, project, release and binding secrets, which
vmmd opens at wake:

1. `gregalectl seal-domain init --domain runtime --dir <dir>`.
2. Set `faas_seal_domain_runtime_identity_src` (compute and single-box hosts
   stage it for vmmd and verify the runtime probe) and
   `faas_seal_domain_runtime_recipient_src` (control-plane hosts give it to
   apid), then converge. vmmd loads the identity at its next start and
   reports `faas_seal_domain_loaded{domain="runtime"} 1`. apid keeps sealing
   to the fleet recipient.
3. Once every vmmd reports 1 and the previous release left the rollback
   window, set `faas_seal_domain_runtime_readers_verified: true` and
   `faas_seal_domain_runtime_writes: domain`, converge, and run
   `gregalectl seal-domain migrate --domain runtime --readers-verified`.

The `source` domain covers GitHub install tokens and webhook secret copies,
which githubd opens. Set `faas_seal_domain_source_identity_src` and
`faas_seal_domain_source_recipient_src` (control-plane hosts stage both) and
converge; the role gives githubd the identity and proves the probe before
apid seals to the recipient. Then set
`faas_seal_domain_source_readers_verified: true` and
`faas_seal_domain_source_writes: domain`, converge, and run
`gregalectl seal-domain migrate --domain source --readers-verified`.

The `build` domain stages like `runtime`: compute hosts get
`faas_seal_domain_build_identity_src` (imaged reads it; the role restarts
imaged and proves the probe), control-plane hosts
`faas_seal_domain_build_recipient_src` (apid seals to it, to both keys until
`faas_seal_domain_build_writes: domain`).

The checklist for every domain, with checks and rollbacks for each step, is
[docs/runbooks/seal-domains-rollout.md](../../../../docs/runbooks/seal-domains-rollout.md).
