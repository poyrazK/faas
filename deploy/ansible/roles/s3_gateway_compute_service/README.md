# s3_gateway_compute_service

The S3 gateway on a compute node (ADR-20261031122757): `s3-gatewayd` in
compute mode (`faas-s3-gatewayd-compute`), listening on loopback for the
cell's s3 tunnel. It holds:

- the storage seal-domain identity alone (the `seal_domains` role stages it),
  never the fleet or host identity;
- the gateway-only provider credential
  (`/etc/faas/secrets/object-storage/gateway-provider.env`), never the
  bucket-admin one;
- the control plane's provider registry with this node's port share as
  `transfer.max_bytes_per_second`.

In compute mode `s3-gatewayd` refuses to start holding more, and runs no
provider usage export. The role runs in the compute play of `bootstrap.yml`
and is off unless the manifest sets `object_storage.compute_nodes: true`.
Turning it off stops and removes the unit, its drop-ins, the registry and the
credential.

The s3 tunnel connector, its firewall rules and the gateway's metrics scrape
come with the next step; until then the gateway runs on loopback and serves
no customer traffic.

## Operator steps per environment

1. Contract the storage seal domain (`docs/runbooks/seal-domains-rollout.md`,
   section 9 and the storage contract step). Then add
   `faas_seal_domain_storage_writes: domain` and
   `faas_seal_domain_storage_contracted: true` to the compute variables
   (`COMPUTE_ANSIBLE_VARS_B64`) as well, and set the environment variable
   `SEAL_DOMAIN_STORAGE_ON_COMPUTE=true`, so CD hands compute converges the
   storage identity. Compute nodes never get its recipient.
2. Give every backend a gateway credential: a second provider user with the
   policy in `deploy/ansible/roles/s3_gateway_service/README.md`, named in the
   registry by `gateway_access_key_env` and `gateway_secret_key_env`. Put only
   those variables in a separate env file on the controller.
3. In the manifest, set on `object_storage`:

   ```yaml
   compute_nodes: true
   gateway_provider_env_src: /operator/secrets/object-storage-gateway.env
   ```

   and `port_mbps` on every compute host (its port speed in Mbit/s). The
   manifest refuses `compute_nodes` without `public_edge.compute_nodes`.
4. Roll out. The node join holds the gateway's 512 MB and one CPU out of
   tenant admission (`gregalectl compute-nodes sizing --edge
   --object-gateway`).

## Checks

- `systemctl status faas-s3-gatewayd-compute`
- `curl -fsS http://127.0.0.1:9096/readyz`
- `journalctl -u faas-s3-gatewayd-compute | grep "storage seal domain"`
  shows `"domain_identity":true` and `"fleet_identity":false`.
- `/etc/faas/secrets/object-storage/provider.env` does not exist.
