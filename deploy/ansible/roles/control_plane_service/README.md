# control_plane_service ansible role

Drops systemd units + example TOMLs for the three non-root control-plane
daemons (apid, schedd, meterd). Does NOT enable or start them — the
operator runs `systemctl enable --now faas-{apid,schedd,imaged}` once
`/etc/faas/sealed.env` is populated with `DATABASE_URL` (gap G2).

The role assumes:

- `deploy/etc/{apid,schedd,imaged}.toml.example` and `deploy/systemd/faas-{apid,schedd,imaged}.service` are co-located with this README in the role's `files/` tree.
- The `faas` group already exists (created by `vmmd_service` or the postgres role).
- `storage.env.example` is installed for the shared OCI contract. A populated
  `/etc/faas/storage.env` is staged by `deploy join-node` and loaded by
  schedd; credentials stay outside inventory and git.

On public-beta `control-plane` hosts, the role also installs
`zz-faas-api-contract-diff.conf` for `faas-apid`. This enables the OpenAPI
contract preview; the matching compute-only drop-in enables the promotion gate
so both paths evaluate the same stored production baseline. The default remains
off on single-box and local installs.
