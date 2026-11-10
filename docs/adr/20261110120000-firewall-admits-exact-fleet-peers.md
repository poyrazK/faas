# ADR-20261110120000: The host firewall admits each fleet peer by its own address

- Status: Accepted
- Date: 2026-11-10
- Related: ADR-20261109100000 (bare-metal private card), ADR-20261108124106
  (vmmd dials name the node they mean), ADR-20261022184531 (node wake
  failover), ADR-110 (split-box manifest)

## Context

Compute nodes run untrusted tenant code. Root on one compute node should reach
as little of the rest of the fleet as possible.

The host firewall admits peers by source address. `gregalectl manifest
ansible` builds the lists from each host's manifest `address`. An IP-literal
address becomes a `/32`. A hostname (production: `fsn-1.gregale.dev:9091`)
cannot be resolved while rendering, and the repo keeps provider IPs out of the
committed manifest and generated files, so the renderer falls back to the whole
`overlay.cidr`. In a hostname fleet every list is the subnet: any host on the
private network reaches the control plane's private services, and every compute
node reaches every other compute node's vmmd, gatewayd, schedd and metrics
ports.

Some compute-to-compute traffic is load-bearing and only works today because
of that subnet rule. Compute nodes run a node-local schedd and register it as
`compute_nodes.schedd_target_url`; gatewayd-internal on any node wakes through
the owning node's schedd (9091) and forwards to the owning node's vmmd (50051).

vmmd also re-renders the compute node's whole ruleset at runtime from its
drop-in (`FAAS_PRIVATE_INGRESS_CIDRS`/`_TCP_PORTS`), so the runtime ruleset
must carry the same sources as the boot ruleset.

## Decision

1. The renderer emits, besides the unchanged `*_allowed_cidrs` lists, an exact
   list per firewall boundary: `{cidrs, hosts}`. IP-literal hosts are `/32`s;
   hostname hosts are inventory names. No address is written for a hostname
   host.
   - control plane: `faas_compute_allowed_exact` (compute hosts; used for the
     control-services and Postgres rules);
   - compute: `faas_control_plane_allowed_exact` and `faas_compute_peer_exact`
     (the other compute hosts).
2. The nftables template resolves each host to the address `/etc/hosts`
   already maps the fleet's names to (`faas_private_dns_address`, else
   `faas_private_address`, else the gathered default IPv4), so the firewall
   admits exactly the address daemons dial. If any host of a list does not
   resolve in the run, that list keeps its `*_allowed_cidrs` value, so a
   partial run never cuts a host off.
3. A new compute-peer rule admits the other compute nodes only to
   `faas_compute_peer_ports` (50051 vmmd, 9091 schedd). The control-plane rule
   no longer covers them, so peers lose gatewayd, egress, private control and
   metrics ports.
4. vmmd reads `FAAS_PRIVATE_CONTROL_INGRESS_CIDRS` (replaces
   `FAAS_PRIVATE_INGRESS_CIDRS` as the control-plane sources when set) and
   `FAAS_PRIVATE_PEER_INGRESS_CIDRS`/`_TCP_PORTS`. The drop-in keeps
   `FAAS_PRIVATE_INGRESS_CIDRS` unchanged, so a vmmd that predates these keeps
   today's ruleset after an Ansible run that lands before the binary.
5. The `*_allowed_cidrs` lists stay as they are for every other reader
   (pg_hba, gatewayd-public trusted ingress, fail2ban, the app edge).

Listeners still bind `0.0.0.0` where they did; binding the private address is
a separate change for listeners without loopback clients. Per-node object-store
credentials follow the S3 artifact store (#6724).

## Consequences

- Production narrows on its next `bootstrap-control-plane`/`bootstrap-compute`
  and vmmd release, with no manifest change. Inventories without the exact
  lists render byte-identically.
- Compute-to-compute access shrinks to vmmd and schedd; both already require
  mTLS, and vmmd dials are bound to the node's certificate name
  (ADR-20261108124106). Removing those two ports needs owner routing, which is
  out of scope.
- An IP-literal multi-compute fleet gains the peer rule. Before, its
  control-plane `/32` blocked cross-node forwards and wakes.
- The resolver macro is copied into the nftables and vmmd templates; a test
  keeps the copies identical.
