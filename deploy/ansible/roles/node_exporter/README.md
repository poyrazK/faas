# `node_exporter` ansible role

Installs the Prometheus `node_exporter` binary pinned to a specific
version, bound to the bridge IP (`10.0.0.1:9100`). The public iface's
input chain policy is `drop`; Prometheus scrapes via the bridge so
that's the only address the exporter needs to listen on.

## Why bridge-only

`nftables` host policy (spec §7) drops unsolicited ingress on the
public iface. The Prometheus scraper dials from inside the box (or
from the operator's admin machine through the bridge), so listening
on the public iface would just be additional attack surface for
zero benefit.

## Collectors disabled

- `--collector.filesystem.mount-points-exclude=^/(sys|proc|dev|run)($|/)`
  — skip pseudo-fs noise.
- `--collector.netclass.ignored-devices=^(veth|docker)` — skip
  container veth pairs (we don't run containers on the host;
  cgroups v2 hosts are isolated by slice instead).
- `--collector.diskstats.ignored-devices=^(loop|dm-)` — skip loop
  + device-mapper noise (loop is the installer; dm-* is lvm, which
  we expose via the custom `fcvm_lv_fc_used_pct` gauge instead).

## Override at invocation

```bash
ansible-playbook -e node_exporter_version=1.9.0 \
                 -e node_exporter_release_sha256=<new-sha> site.yml
```