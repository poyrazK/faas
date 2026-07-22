# faas_overlay — issue #98 / ADR-028

Installs and configures the cross-box overlay so gatewayd can reach
remote vmmd boxes. Two providers:

- `tailscale` (default) — authkey-based, one operator action.
- `wireguard` (stub) — operator-managed peers + keys.

## Operator setup

Tailscale path:

1. Create an authkey in your tailnet admin console.
2. Drop the value at `/etc/faas/secrets/tailscale.authkey` (0640 root:faas).
3. Run the role. The systemd unit brings the tunnel up on boot.

Wireguard path:

1. Mint keys per box: `wg genkey | tee privatekey | wg pubkey > publickey`.
2. Exchange public keys with peer operators out-of-band.
3. Vault the private key + render the role with `faas_overlay_private_key`
   and `faas_overlay_peers` populated via ansible-vault.
4. Run the role.

## Why both providers

Tailscale is the default because it cuts the operational
complexity for the canonical multi-node case (small fleet, one
operator). Wireguard is the path a multi-tenant / air-gapped /
regulated operator needs when tailscale's hosted control plane
isn't acceptable.

The role does not pretend to be smart — it pins one provider per
run. A box running tailscale today can switch to wireguard by
re-running the role with `-e faas_overlay_provider=wireguard`
once the operator has provisioned keys + peers.

## Files

```
defaults/main.yml          — operator knobs (provider, iface, authkey path)
tasks/main.yml             — provider dispatch + cross-provider firewall
tasks/tailscale.yml        — apt repo + tailscale install
tasks/wireguard.yml        — wireguard install (operator-vaulted keys)
templates/faas-overlay-tailscale.service.j2
templates/faas-overlay-wireguard.service.j2
templates/faas-overlay-wireguard.conf.j2
handlers/main.yml          — systemd reload + nftables reload
```

## Verification

After `ansible-playbook`:

- `systemctl status faas-overlay` shows `active (exited)` once the
  one-shot completes.
- `tailscale status` (Tailscale) or `wg show` (Wireguard) lists the
  interface with an IP.
- `nc -vz <peer-overlay-ip> 50051` from gatewayd's box succeeds.
