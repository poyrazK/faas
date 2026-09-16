# `node_exporter` ansible role

Installs the Prometheus `node_exporter` binary pinned to a specific version.
Control-plane and single-box hosts bind loopback. Compute hosts bind their
private fleet address so the control-plane Prometheus can discover every
host's textfile metrics from the active `compute_nodes` registry.

## Network boundary

`nftables` host policy admits compute TCP/9100 only from configured
control-plane CIDRs. The public interface remains unreachable. This lets the
central scraper collect per-host certificate expiry while preserving the
same private fleet boundary as the daemon metrics ports.

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
                 -e node_exporter_release_sha256=<new-sha> bootstrap.yml
```
