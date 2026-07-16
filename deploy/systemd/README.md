# gatewayd deploy

This directory contains the systemd unit and nftables ruleset fragment for
gatewayd, the only public listener on the box (spec §4.1).

## Install

```
sudo install -m 0644 deploy/systemd/faas-gatewayd.service /etc/systemd/system/
sudo install -m 0644 deploy/nftables/gatewayd.nft /etc/nftables.d/
sudo systemctl daemon-reload
sudo nft -f /etc/nftables.d/gatewayd.nft
sudo systemctl enable --now faas-gatewayd
sudo systemctl status faas-gatewayd
```

## SIGHUP

```
sudo systemctl reload faas-gatewayd
```

Drops in-memory rate-limit buckets (Limiter.ForgetAll). Safe and idempotent.

## Memory cap

512 MB (`MemoryMax=512M`) per the control-plane budget table at
spec §13. OOMs in the control-plane slice are deliberately segregated
from tenant failures — see spec §13.

## Hardening

`User=faas`, `NoNewPrivileges=yes`, `ProtectSystem=strict`, namespaces
and syscall sets locked down. If a future change needs to bind to
something outside the allow-list, document the rationale in the PR.
