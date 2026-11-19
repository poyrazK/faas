# Tenant egress gateway (ADR-372)

Tenant IPv4 egress leaves through one WireGuard gateway, so abuse reports name
the gateway's address and not a compute node's. The gateway is optional; this
runbook applies to fleets whose manifest declares `egress.tenant_gateway`.

## Bring-up

1. Create a small Linux VM with a public IPv4 address, ideally at a provider
   whose terms allow a multi-tenant egress IP. Open UDP to the WireGuard port
   (51820 in the examples) from the compute nodes, and SSH for Ansible.
2. On the gateway, mint its key and print the public half:

   ```bash
   sudo sh -c 'umask 077; wg genkey > /etc/wireguard/wg-tenant.key'
   ```

   ```bash
   sudo wg pubkey < /etc/wireguard/wg-tenant.key
   ```

   If `wg` is not installed yet, skip this step. The first converge mints the
   key and fails with a message that prints it.
3. Add `egress.tenant_gateway` to the manifest (`endpoint`, `public_key`,
   `tunnel_cidr`, optional `ssh_host`) and regenerate the inventory:

   ```bash
   make manifest-ansible MANIFEST=deploy/manifest/splitbox.yaml
   ```

4. Converge the gateway, then every compute node:

   ```bash
   make ANSIBLE_INVENTORY=deploy/ansible/.generated/inventory/hosts.ini bootstrap-tenant-egress-gateway
   ```

5. Re-run `bootstrap-compute` so vmmd picks up `FAAS_TENANT_EGRESS_IFACE` and
   the host firewall accepts tenant traffic only through the tunnel.

Order matters: a compute node converged with the gateway declared routes
tenant egress into the tunnel at once, and that egress fails until its peer is
live on the gateway. `bootstrap-tenant-egress-gateway` registers the peer
before it starts each node's tunnel.

## Verify

On a compute node:

```bash
sudo wg show wg-tenant latest-handshakes
```

```bash
ip rule show | grep 51820
```

From a tenant app, fetch an IP-echo service. The answer must be the gateway's
address.

On the gateway:

```bash
sudo nft list table inet faas_tenant_gw
```

`faas_gw_forwarded` should grow with tenant traffic. `faas_gw_deny_private`
and `faas_gw_deny_smtp` should stay near zero, because the compute nodes drop
that traffic first. A rising count means a node's own policy is missing.

## Symptoms

- **All tenant egress times out, inbound still works.** The tunnel is down.
  This is the designed failure mode (ADR-372 decision 7): there is no
  fallback to the node's address. Check `wg show` for a recent handshake on
  both ends, the gateway's UDP port, and `systemctl status wg-quick@wg-tenant`.
- **One node's tenants have no egress after a join.** Its peer file is
  missing on the gateway (`/etc/faas/tenant-egress/peers/<node>.conf`). Re-run
  `bootstrap-tenant-egress-gateway`.
- **Large responses stall, small ones work.** MTU. Both clamps must be in
  place: the compute host forward chain (`maxseg size set rt mtu` on
  `wg-tenant`) and the gateway `clamp` chain.
- **Converge fails with "the manifest pins ...".** The gateway's key does not
  match `egress.tenant_gateway.public_key`. Put the printed key in the
  manifest and regenerate, or restore the old key file from backup.

## Replacing or re-addressing the gateway

The gateway has no state besides its key. To move it, bring up the new VM,
set the new `endpoint`, `ssh_host` and `public_key` in the manifest,
regenerate, and run `bootstrap-tenant-egress-gateway`. The compute tunnels
switch when their config is re-rendered. Tenant egress is down between the
old gateway going away and the compute converge finishing.

## Abuse reports

An abuse report now names the gateway's address. Attribute it with the egress
flow log (ADR-371) by destination and time. The flow log records the tenant's
own bridge address, not the gateway's.

## Turning it off

Remove `egress.tenant_gateway` from the manifest, regenerate and re-run
`bootstrap-compute`. Each node stops its tunnel and tenant egress leaves from
the node's own interface again.
