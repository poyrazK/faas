# Static egress IP

Send an app's outbound traffic from a fixed public IPv4 address, for upstream
services that only accept allowlisted addresses (partner APIs, managed
databases, payment gateways).

Static egress is included in the Scale plan: each app can pin one address.
Addresses are assigned to your account by Gregale support; ask for one, then
pin it:

```bash
gregale app APP_ID static-egress-ip show            # your assigned addresses and the current pin
gregale app APP_ID static-egress-ip set 203.0.113.42
gregale app APP_ID static-egress-ip clear
```

`show` lists `available_ips`, the addresses assigned to your account. Only
those can be pinned, and each address can be pinned by one app at a time; clear
it on one app before pinning it on another.

## What changes

- Outbound connections from the app's production deployment leave from the
  pinned address. Inbound routing is unchanged.
- Preview and stage deployments keep leaving through shared egress, so a
  partner allowlist for production's address never accepts stage traffic.
- The address is routed to one Gregale compute node, and a pinned app runs on
  that node. If that node is unavailable, the app cannot start until it
  returns; it never runs elsewhere from a different address. An instance that
  was already running on another node when you pinned cannot send until it
  moves: run `gregale app APP_ID restart` after pinning.
- Pinned apps cannot be split across routes (`route split` refuses them).

## Changing or removing a pin

Add the address to the upstream allowlist before you pin it, then verify with
a safe endpoint, for example one that echoes your source address. Clearing or
changing a pin takes effect on running instances within seconds and can break
allowlisted integrations, so treat it as a planned change.

If your account leaves the Scale plan, the pin is kept but inactive (`active:
false`): the app leaves through shared egress until you upgrade again. If you
delete an app, its pin is released.

Do not hard-code the address in application code; read it from your own
configuration so a later change only touches the allowlist.

IPv4 only.
