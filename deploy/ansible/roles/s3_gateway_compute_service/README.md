# s3_gateway_compute_service

The S3 gateway on a compute node (ADR-20261031122757): `s3-gatewayd` in
compute mode (`faas-s3-gatewayd-compute`), listening on loopback
(`127.0.0.1:8084`) for the node's s3 tunnel connector, `faas-cloudflared-s3`.
It holds:

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
Turning it off stops and removes the gateway and its connector, their units and
drop-ins, the registry and the credential; the s3 tunnel token stays until the
operator revokes it.

## The s3 tunnel connector

`faas-cloudflared-s3` is the node's replica of the cell's second remotely
managed tunnel, whose ingress rule (set in Cloudflare) sends `s3.<domain>` to
`http://127.0.0.1:8084`. It runs the cloudflared binary
`gatewayd_public_edge_service` installs, so the manifest requires
`public_edge.compute_nodes` with `object_storage.compute_nodes`. It is:

- bound to the gateway (`BindsTo=`, `PartOf=`, `WantedBy=` the gateway): it
  starts once the gateway reports ready, leaves the tunnel whenever the
  gateway stops, and on a stop drains through the still-running gateway;
- stopped until its token exists at
  `/etc/faas/secrets/cloudflared/s3-tunnel-token` (root, 0600), which the
  operator stages. It is a separate token from the app tunnel's.

No S3 traffic reaches the tunnel until `s3.<domain>` moves to it (ADR step 6).

## Metrics and firewall

The gateway's control listener binds `0.0.0.0:9096` on a compute node and the
connector's metrics `0.0.0.0:20242`; the data listener stays on loopback.
nftables admits only the control plane to the two ports. The control plane's
Prometheus discovers both per node from apid
(`/v1/internal/metrics/s3-gatewayd-compute-targets`,
`/v1/internal/metrics/cloudflared-s3-targets`) while the manifest runs the
gateway on compute nodes; `FaasComputeObjectGatewayDown`,
`FaasComputeObjectGatewayConnectorDisconnected` and
`FaasS3GatewayPortShareSaturated` cover them
(`docs/runbooks/FaasObjectStorage.md`).

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
4. Create the cell's s3 tunnel in Cloudflare with the ingress rule above and
   stage its token on every compute node, so connectors join as soon as the
   gateways start; a node without it stays in
   `FaasComputeObjectGatewayConnectorDisconnected`.
5. Roll out, once the nodes run a release with compute mode (its listener
   checks refuse the unit's control address otherwise). The node join holds
   the gateway's 512 MB and one CPU out of tenant admission (`gregalectl
   compute-nodes sizing --edge --object-gateway`).

## Checks

- `systemctl status faas-s3-gatewayd-compute faas-cloudflared-s3`
- `curl -fsS http://127.0.0.1:9096/readyz`
- `curl -fsS http://127.0.0.1:20242/ready` answers once the connector holds
  tunnel connections.
- `journalctl -u faas-s3-gatewayd-compute | grep "storage seal domain"`
  shows `"domain_identity":true` and `"fleet_identity":false`.
- `/etc/faas/secrets/object-storage/provider.env` does not exist.
