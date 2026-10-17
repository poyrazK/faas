# Tenant egress denylist

<!-- GENERATED — do not edit by hand; regenerate with `make denylist-md`. -->

Single source of truth: [`pkg/netns/denylist.go::NewDefaultDenySet()`](pkg/netns/denylist.go).
The OCI-only section is the typed `ociOnlyDenyCIDRsV4` array in [`pkg/oci/egress.go`](pkg/oci/egress.go).

## Platform-wide catalog

Enforced by all three sinks: per-netns nftables (table `ip faas` / `ip6 faas`, chain `forward`), host nftables (table `inet faas`, chain `forward`), and the OCI user-space dialer. Cross-renderer invariant pinned by `pkg/netns/denylist_external_test.go::TestAllThreeConsumersAgreeOnDenySet`.

### IPv4 CIDRs

| CIDR | Source | Rationale | Test pin |
|------|--------|-----------|----------|
| `10.0.0.0/8` | spec-§11 | RFC1918 — private network | `pkg/netns/denylist_external_test.go::TestAllThreeConsumersAgreeOnDenySet` |
| `100.64.0.0/10` | RFC6598 | carrier-grade NAT | `pkg/netns/denylist_external_test.go::TestAllThreeConsumersAgreeOnDenySet` |
| `169.254.0.0/16` | spec-§11 | link-local; 169.254.169.254 = cloud metadata IMDS | `pkg/netns/denylist_external_test.go::TestAllThreeConsumersAgreeOnDenySet` |
| `172.16.0.0/12` | spec-§11 | RFC1918 — private network | `pkg/netns/denylist_external_test.go::TestAllThreeConsumersAgreeOnDenySet` |
| `192.168.0.0/16` | spec-§11 | RFC1918 — private network | `pkg/netns/denylist_external_test.go::TestAllThreeConsumersAgreeOnDenySet` |

### IPv6 CIDRs

| CIDR | Source | Rationale | Test pin |
|------|--------|-----------|----------|
| `2001::/32` | ADR-034 | Teredo (RFC4380); tunnels IPv6 over UDP/3544 — same lateral-movement risk as 6to4 | `pkg/netns/denylist_external_test.go::TestAllThreeConsumersAgreeOnDenySet` |
| `2002::/16` | ADR-034 | 6to4 (RFC3056); tunnels IPv6 over IPv4 — lateral movement into 10/8 etc. | `pkg/netns/denylist_external_test.go::TestAllThreeConsumersAgreeOnDenySet` |
| `::/128` | ADR-023 | IPv6 unspecified; misconfigured or malicious | `pkg/netns/denylist_external_test.go::TestAllThreeConsumersAgreeOnDenySet` |
| `::1/128` | ADR-023 | IPv6 loopback | `pkg/netns/denylist_external_test.go::TestAllThreeConsumersAgreeOnDenySet` |
| `fc00::/7` | ADR-023 | IPv6 ULA (RFC4193); control-plane lateral movement | `pkg/netns/denylist_external_test.go::TestAllThreeConsumersAgreeOnDenySet` |
| `fe80::/10` | ADR-023 | IPv6 link-local; neighbor-table exposure to guests | `pkg/netns/denylist_external_test.go::TestAllThreeConsumersAgreeOnDenySet` |
| `ff00::/8` | ADR-023 | IPv6 multicast; no use case in this model | `pkg/netns/denylist_external_test.go::TestAllThreeConsumersAgreeOnDenySet` |

### SMTP TCP ports

| Port | Source | Rationale | Test pin |
|------|--------|-----------|----------|
| `25` | `spec-§11` | spam = Hetzner abuse desk = existential (spec §7 founding doc R6) | `pkg/netns/denylist_test.go::TestNewDefaultDenySet_SMTPPortsAreComplete`, `pkg/netns/denylist_external_test.go::TestAllThreeConsumersAgreeOnSMTPPorts` |
| `465` | `spec-§11` | spam = Hetzner abuse desk = existential (spec §7 founding doc R6) | `pkg/netns/denylist_test.go::TestNewDefaultDenySet_SMTPPortsAreComplete`, `pkg/netns/denylist_external_test.go::TestAllThreeConsumersAgreeOnSMTPPorts` |
| `587` | `spec-§11` | spam = Hetzner abuse desk = existential (spec §7 founding doc R6) | `pkg/netns/denylist_test.go::TestNewDefaultDenySet_SMTPPortsAreComplete`, `pkg/netns/denylist_external_test.go::TestAllThreeConsumersAgreeOnSMTPPorts` |

## OCI-only client hardening

These ranges are enforced by the OCI user-space dialer ONLY. They are intentionally NOT in the shared catalog because the host firewall does not need them (no tenant process binds to loopback from the OCI puller, etc.). They are process-level defence-in-depth: if the firewall ever regresses, the user-space check still refuses the dial. Pinned by `pkg/oci/egress_test.go`.

Note: the `0.0.0.0/8` and `127.0.0.0/8` entries are also denied by the `IsLoopback` / `IsUnspecified` predicate in `pkg/oci/egress.go::ipAllowed` (the address-class layer). They are restated here as explicit ranges so the user-space check remains a deny even if the predicate is ever refactored. `192.0.0.0/24`, `198.18.0.0/15`, and `240.0.0.0/4` are NOT covered by the predicate and rely on these entries alone.

### IPv4 CIDRs (OCI-only)

| CIDR | Source | Rationale | Test pin |
|------|--------|-----------|----------|
| `0.0.0.0/8` | ADR-034 | unspecified IPv4 source range (defence-in-depth) | `pkg/oci/egress_test.go::TestIPAllowed_OCIOnlyEntriesDenied` |
| `127.0.0.0/8` | ADR-034 | loopback range; OCI puller runs outside tenant netns | `pkg/oci/egress_test.go::TestIPAllowed_OCIOnlyEntriesDenied` |
| `192.0.0.0/24` | ADR-034 | IETF protocol assignments | `pkg/oci/egress_test.go::TestIPAllowed_OCIOnlyEntriesDenied` |
| `198.18.0.0/15` | ADR-034 | benchmarking range | `pkg/oci/egress_test.go::TestIPAllowed_OCIOnlyEntriesDenied` |
| `240.0.0.0/4` | ADR-034 | reserved IPv4 range | `pkg/oci/egress_test.go::TestIPAllowed_OCIOnlyEntriesDenied` |
