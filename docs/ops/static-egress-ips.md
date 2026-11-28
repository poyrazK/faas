# Static egress addresses (operator runbook)

ADR-20261128090000. A static egress address is an additional public IPv4 the
provider routes to one compute node. Operators keep the inventory with
`gregalectl static-egress-ips`; customers on Scale pin an address assigned to
their account (`gregale app APP static-egress-ip set IP`).

The customer API is behind the `static_egress_ip` platform flag
(`gregalectl flags`); it answers 402 while the flag is off for the account.

## Offer an address to a customer

1. Order an additional IP for the node from the provider (OVHcloud
   "Additional IP" routed to the server; set its reverse DNS if the customer's
   partner checks PTR records). The node needs no interface configuration:
   vmmd aliases the address on `br-tenants` itself.
2. Record it and assign it:

   ```bash
   gregalectl static-egress-ips set --ip 203.0.113.42 --node bhs-2 \
     --account-id <account uuid> --note "ovh order 12345"
   gregalectl static-egress-ips list
   ```

   vmmd on `bhs-2` picks the change up within seconds
   (`vmmd: static egress inventory applied` in its log).
3. Tell the customer the address. They pin it on one app.

## Move an address to another node

Move the IP at the provider first, then:

```bash
gregalectl static-egress-ips set --ip 203.0.113.42 --node bhs-3 --account-id <same account>
```

The pin follows. Running instances on the old node keep sending from the
address until they park, after which the provider no longer routes it there;
park the pinned app (`gregalectl instances force-park`) to move it at once.
New wakes land on the new node.

## Unassign or remove

Reassigning to another account or removing is refused (409) while a live app
pins the address: ask the customer to clear the pin, or clear it for them. A
deleted app's pin does not hold the address.

```bash
gregalectl static-egress-ips set --ip 203.0.113.42 --node bhs-2   # unassigned, kept
gregalectl static-egress-ips remove --ip 203.0.113.42 --yes
```

## Behaviour to know

- A pinned production deployment is placed only on its address's node. When
  that node is down or drained, wakes are refused with the
  `static_egress_node` admission blocker; live migration does not move pinned
  instances off it.
- Pinned traffic leaves through the node's public interface, not the tenant
  egress gateway (ADR-372), so abuse reports for it name the node provider's
  address space.
- vmmd restart adoption keeps pinned guests (their SNAT is rebuilt before the
  first host render).
- A pin on an account that left Scale is inactive; the app uses shared egress.

## Verify a real address

`TestMetalStaticEgressPacketFlow` checks the rendered rules in network
namespaces. After a new provider or node type, check a real address end to
end once: pin it on a test app that returns an HTTP echo of its outbound
source (for example `curl -s https://ifconfig.me` from the app), and confirm
the answer is the pinned address and that an unpinned app on the same node
answers the node's (or the egress gateway's) address.

## Legacy TOML bundle

`/etc/faas/egress/static_egress_ips.toml` (vmmd `static_egress_ip_bundle`)
still grants addresses on the node it is on, beside the inventory. Pins on
bundle-only addresses are not placement-constrained (they predate multi-node).
Prefer the inventory.
