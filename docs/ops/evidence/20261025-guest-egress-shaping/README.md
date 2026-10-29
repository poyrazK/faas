# Guest egress shaping evidence — 2026-10-25 (#4382)

Host: `gregale-internal-test-1` (n2-highmem-2, nested KVM, Firecracker 1.17.0).
Nested KVM is functional evidence only; native x86_64 acceptance runs when the
native CI hosts are back.

## Direction and rate (`egress-throughput.sh`)

Two private namespaces and a veth pair stand in for an instance: one end plays
`VethPeer` (the instance side), the other `VethHost`. The script applies the
argv `netns.Config.TcCommands` produces, before the fix (`VethHost` only) and
after (`VethHost` plus `VethPeer`), then times a 24 MiB TCP transfer each way.
Egress is instance to host; ingress is host to instance.

| Cap | Direction | No qdisc | Before (`VethHost` only) | After (both ends) |
|---|---|---|---|---|
| — | egress | 32,545 Mbit/s | — | — |
| — | ingress | 33,427 Mbit/s | — | — |
| 25 Mbit | egress | — | **37,267 Mbit/s (unshaped)** | **23.8 Mbit/s** |
| 25 Mbit | ingress | — | 23.9 Mbit/s | 23.9 Mbit/s |
| 250 Mbit | egress | — | **13,113 Mbit/s (unshaped)** | **239.2 Mbit/s** |
| 250 Mbit | ingress | — | 238.6 Mbit/s | 239.3 Mbit/s |

Before the fix the cap shaped only traffic toward the instance. After it, both
directions land within 5% of the plan rate (#31's acceptance bound is ±10%),
with the existing `burst 32kbit / latency 400ms` parameters. The script removed
its namespaces on exit.

## Metal suite (`egress-metal.sh`)

Source `35ce7a8c` (`fix/guest-egress-shaping`), run under the shared
`/run/lock/faas-builder-acceptance.lock` with a private tmpfs jail.

```text
leakcheck before: rc=0 leakcheck OK — no leaked netns/taps/jails/cgroups/processes/bridges/mounts/native-loops
--- PASS: TestMetalEgressCapEnforced (8.75s)
--- PASS: TestMetalNativeNetworkHelperProtocol (4.77s)
--- SKIP: TestMetalFreshNetworkPolicy (0.00s)  requires private mount/network namespaces and /run/netns
leakcheck after: rc=0 leakcheck OK — no leaked netns/taps/jails/cgroups/processes/bridges/mounts/native-loops
```

`TestMetalEgressCapEnforced` cold-boots a Hobby (25 Mbit) instance and reads
the live qdisc on both `VethHost` and, inside the instance namespace, `VethPeer`.
`TestMetalNativeNetworkHelperProtocol` runs the network commands, including the
new `ip netns exec … tc` argv, through the native host helper.
