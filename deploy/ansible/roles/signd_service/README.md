# signd_service

Installs `signd`, the control plane's artifact signer (ADR-20261109170000).

`signd` holds the fleet signing key (`/etc/faas/secrets/sign.key`) and signs
the app and sidecar layers compute nodes build, once per storage key and only
for the node building that deployment.

Off by default. To turn it on:

1. Set `faas_signd_listen: "tcp://<control plane private address>:8097"` on the
   control plane and converge. The role:
   - creates the `faas-signd` user;
   - issues signd's server leaf (pkg/pki feature `artifact-signer`);
   - installs the unit and its drop-in.

   CD starts signd from the active release, and the host firewall opens 8097
   to the compute CIDRs.
2. Set `faas_imaged_signd_target` to the same address on the compute nodes and
   converge. `compute_only_service` issues each node's client leaf on the
   control plane, and imaged signs app layers through signd
   (`FAAS_ARTIFACT_SIGNER=signd`).

signd logs in to Postgres as `faas_signd`, through peer authentication as
`faas-signd`. It is the only role that may insert into `artifact_signatures`.
