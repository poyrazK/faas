# ADR-20261128090000 · Static egress addresses: operator inventory, node placement, restart adoption

- **Status:** accepted
- **Date:** 2026-11-28
- **Amends:** ADR-119 (static outbound IP), ADR-20261103194727 (runtime SNAT from lease sources), ADR-20261017 (vmmd restart adoption)
- **Supersedes for static egress:** ADR-171, ADR-184, ADR-185 (provider-neutral reserved IP leases, never wired to a consumer)
- **Scope:** `pkg/state`, `pkg/apid/staticegress`, apid static egress handlers, `gregalectl static-egress-ips`, schedd placement and live migration, vmmd grants and adoption

## Context

ADR-119 shipped the customer API, and ADR-20261103194727 fixed the data path
(SNAT from each VM's real lease address). A review on 2026-11-28 found the
feature still could not be used:

1. **No provisioning path.** An address counted as provisioned only when it
   was listed in a hand-written TOML file on a node
   (`/etc/faas/egress/static_egress_ips.toml`). No deploy role writes it, so
   every pin answered `404 static_egress_ip_not_provisioned`.
2. **No placement.** The provider routes an additional IP to one server, but
   schedd placed a pinned app on any node. The pin gate accepted an address
   provisioned on *any* node, so a wake elsewhere failed with
   `ErrStaticEgressUnprovisioned`.
3. **Restarts dropped pinned guests.** vmmd restart adoption refused them
   (`static egress SNAT is not rebuilt yet`), so every vmmd release
   cold-restarted every pinned app.
4. **Downgrades kept the pin**, and a deleted app's pin blocked the address
   for every other app (the unique index ignores `status`).

## Decision

1. **Addresses live on compute nodes; placement follows them.** Each address
   is an additional public IPv4 the provider routes to one compute node. A
   production deployment that pins an inventory address is placed only on
   that node (`Request.StaticEgressPinned` / `StaticEgressNodeIDs`, a gate,
   unlike the locality hints). If the node is unavailable the wake is refused
   with the `static_egress_node` admission blocker; it never runs elsewhere and
   leaves from the wrong address. Live migration does not move a pinned
   instance to another node (`ErrStaticEgressNodeOnly`); it parks and wakes
   on the address's node. Pinned traffic keeps the ADR-20261103194727
   exception from the tenant egress gateway (ADR-372): it leaves through the
   node's public interface, so abuse reports for it name the compute
   provider's address space. Moving an address between nodes is an operator
   action (the provider's API, then `gregalectl static-egress-ips set --node`).
2. **A Postgres inventory, managed hot by operators.** `static_egress_addresses`
   (`ip` PK, `node_id`, nullable `account_id`, `note`) is written only by apid
   through `GET/PUT/DELETE /v1/admin/ops/static-egress-ips[/{ip}]`
   (`gregalectl static-egress-ips list|set|remove`). An address is assigned to
   an account; the account's apps may pin it. Removing an address, or
   reassigning it to another account, is refused (409) while a live app pins
   it; a node move is allowed and the pin follows. apid notifies
   `static_egress_addresses_changed`; vmmd rereads its node's assigned rows on
   the notice and every 5 s, keeps the last good set when a read fails, and
   repairs its policies after a change. The pin gate reads the inventory beside
   the legacy TOML grants, which keep working unchanged and stay unconstrained
   by placement (they were single-node).
3. **Included in Scale.** No charge or meter; the plan gate is unchanged
   (Scale, one address per app).
4. **Effective pin = recorded pin while the plan allows it.**
   `state.EffectiveStaticEgressIP` is the one rule schedd's wake spec and
   vmmd's intent read share. A pin kept after a downgrade is inactive (the app
   leaves through shared egress) and becomes active again on an upgrade. The
   GET response reports `active` and the account's `available_ips`.
5. **Deleted apps release their pin** when another app pins the address, or
   when an operator removes or reassigns it.
6. **Adoption rebuilds static egress.** A survivor's record already carries
   its pin; adoption restores the app's pin, account, reservation and bridge
   alias before registering the guest. The host render at the end of
   adoption, the first after restart, then emits the guest's SNAT from its
   surviving lease. A recorded block is restored as a block. The periodic
   reconcile re-checks the durable intent and the grants afterwards. A record
   without its app or account is still refused.

## Consequences

- An operator can offer static egress without touching a node: order the
  address from the provider for a node, `gregalectl static-egress-ips set
  --ip A --node N --account-id X`, and the customer pins it.
- A pinned app is as available as its address's node. That is the price of
  provider-routed addresses without BGP or a floating-IP layer.
- The netns packet-flow test (`TestMetalStaticEgressPacketFlow`) proves the
  rendered rules send a pinned lease from its address and others from the
  node address, with a simulated upstream. A real provider-routed address is
  still a manual check (docs/static-egress-ip.md runbook).

## Rejected alternatives

- **Addresses on the egress gateway** (any node, no placement): a larger
  data-path rewrite that makes every pinned app depend on the gateway.
- **Ansible-managed TOML:** every change needs a converge (operator values
  belong in hot configuration).
- **Wiring ADR-171/184/185 leases:** they model route movement no connector
  implements; the inventory above is the part that is needed.
