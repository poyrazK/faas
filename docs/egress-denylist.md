# Blocked outbound destinations

<!-- GENERATED — do not edit by hand; regenerate with `make denylist-md`. -->

Apps can't open connections to the addresses and ports on this page. The
rules apply to every app on every plan, and an egress allowlist entry does not
open them. See [Networking](networking.md#outbound-connections) for what an app
can reach.

## Blocked address ranges

### IPv4

| Range | What it is |
|---|---|
| `10.0.0.0/8` | RFC1918 — private network |
| `100.64.0.0/10` | Carrier-grade NAT |
| `169.254.0.0/16` | Link-local, including the cloud metadata address `169.254.169.254` |
| `172.16.0.0/12` | RFC1918 — private network |
| `192.168.0.0/16` | RFC1918 — private network |

### IPv6

| Range | What it is |
|---|---|
| `2001::/32` | Teredo (RFC4380) |
| `2002::/16` | 6to4 (RFC3056) |
| `64:ff9b:1::/48` | NAT64 local-use prefix (RFC8215) |
| `64:ff9b::/96` | NAT64 well-known prefix (RFC6052) |
| `::/128` | IPv6 unspecified |
| `::1/128` | IPv6 loopback |
| `fc00::/7` | IPv6 ULA (RFC4193) |
| `fe80::/10` | IPv6 link-local |
| `ff00::/8` | IPv6 multicast |

## Blocked ports

These TCP ports stay closed, also to destinations on an app's egress
allowlist, and `gregale app egress-ports add` refuses them.

| Ports | What they are |
|---|---|
| `22`, `23`, `3389`, `5900` | Remote administration (SSH, Telnet, RDP, VNC) |
| `25`, `2525` | Outbound mail (SMTP). Send mail through your provider's HTTPS API instead. |
| `53`, `853` | DNS and DNS over TLS. Name lookups always go to Gregale's resolver. |
| `135`, `137`, `138`, `139`, `445` | Windows RPC and file sharing (SMB) |
| `3333`, `4444`, `5555`, `7777`, `14433`, `14444`, `45700` | Cryptocurrency mining pools |
| `6660`, `6661`, `6662`, `6663`, `6664`, `6665`, `6666`, `6667`, `6668`, `6669`, `6697` | IRC |

Mail submission ports `465` and `587` are open only to destinations on the app's egress
allowlist, so an app can send through a mail provider it lists explicitly.

## Image pulls

When Gregale pulls a container image for a deployment, it also refuses a
registry that resolves to one of these ranges:

| Range | What it is |
|---|---|
| `0.0.0.0/8` | Unspecified IPv4 source range |
| `127.0.0.0/8` | Loopback range |
| `192.0.0.0/24` | IETF protocol assignments |
| `198.18.0.0/15` | Benchmarking range |
| `240.0.0.0/4` | Reserved IPv4 range |
