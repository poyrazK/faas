# secretd_service

Installs `secretd`, the control plane's opener of customer runtime secrets
(ADR-20261110210000 §3).

`secretd` holds the runtime seal domain's identity
(`/etc/faas/secrets/domains/runtime.age`) and opens an app's sealed runtime
secrets only for the compute node that owns the app, or a peer the owner
granted. It records every open in `secret_open_audit` before any plaintext
leaves.

Off by default. To turn it on:

1. Set the GitHub environment variable `SEAL_DOMAIN_RUNTIME_ON_CONTROL_PLANE`
   to `true`, so CD stages the runtime identity on the control plane too.
2. Set `faas_secretd_listen: "tcp://<control plane private address>:8098"` on
   the control plane and converge. The role:
   - creates the `faas-secretd` user;
   - issues secretd's server leaf (pkg/pki feature `secret-opener`);
   - installs the unit and its drop-in.

   CD starts secretd from the active release, and the host firewall opens
   8098 to the compute CIDRs.
3. Compute nodes call secretd from slice 3c on (vmmd, behind a flag). Until
   then nothing calls it. Once every vmmd does, node join stops staging the
   runtime identity on compute nodes.

secretd logs in to Postgres as `faas_secretd`, through peer authentication as
`faas-secretd`. It is the only role that may write `secret_grants` and
`secret_open_audit`.

Runbook: `docs/runbooks/secretd.md`.
