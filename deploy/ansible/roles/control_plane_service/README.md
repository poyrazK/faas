# control_plane_service ansible role

Drops systemd units + example TOMLs for the three non-root control-plane
daemons (apid, schedd, imaged). Does NOT enable or start them — the
operator runs `systemctl enable --now faas-{apid,schedd,imaged}` once
`/etc/faas/sealed.env` is populated with `DATABASE_URL` (gap G2).

The role assumes:

- `deploy/etc/{apid,schedd,imaged}.toml.example` and `deploy/systemd/faas-{apid,schedd,imaged}.service` are co-located with this README in the role's `files/` tree.
- The `faas` group already exists (created by `vmmd_service` or the postgres role).
