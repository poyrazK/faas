# FaasConntrackTableFilling

Source: `deploy/ansible/roles/prometheus/files/faas.rules.yml`.
Metrics: `node_nf_conntrack_entries` and `node_nf_conntrack_entries_limit`
(node_exporter's conntrack collector).
Decision: ADR-20261103163954 §4.

## Symptom

A node's conntrack table has been over 80% full for 5 minutes. The table is
shared: public connections to the edge, the control plane's and peers'
private traffic, and every tenant VM's NAT. When it fills the kernel drops
new connections of all of them; tenants see outbound connects time out.

On a node with public entry addresses (`faas_public_entry_addrs`) the usual
cause is a SYN flood with spoofed sources: each SYN leaves an entry in
`SYN_RECV`. The nftables role sizes the table
(`faas_public_entry_conntrack_max`) and shortens that timeout
(`faas_public_entry_conntrack_syn_recv_timeout`) for this.

## Check

```bash
sudo conntrack -C
sysctl net.netfilter.nf_conntrack_max net.netfilter.nf_conntrack_tcp_timeout_syn_recv
sudo conntrack -L -p tcp --state SYN_RECV 2>/dev/null | wc -l
sudo conntrack -L -p tcp --dport 443 2>/dev/null | awk '{for(i=1;i<=NF;i++) if ($i ~ /^src=/) {print $i; break}}' | sort | uniq -c | sort -rn | head
sudo nft list chain inet faas input | grep 'per source'
```

Many `SYN_RECV` entries from many sources: a spoofed SYN flood. Many
established entries from few sources: a connection flood that the
per-source backstop should already cap (check its counters).

## Recover

- A flood on the entry addresses: follow
  `docs/runbooks/EdgeDirectEntry.md`, "Under attack: back behind
  Cloudflare". Step 2 (`gcp-compute-edge-address.sh unentry`) stops it at
  the provider, before the node.
- Room now, without a release: `sudo sysctl -w
  net.netfilter.nf_conntrack_max=<higher>` (memory grows only as entries
  do), and lower `nf_conntrack_tcp_timeout_syn_recv` while a SYN flood
  lasts. The next converge restores the role's values.
- No public entry address and no flood: look for a tenant opening
  connections in a loop (`conntrack -L` by tenant source in
  10.100.0.0/16) before raising the limit.
