from typing import Literal

PrivateNetworkFirewallRuleProtocol = Literal["icmp", "tcp", "udp"]

PRIVATE_NETWORK_FIREWALL_RULE_PROTOCOL_VALUES: set[PrivateNetworkFirewallRuleProtocol] = {
    "icmp",
    "tcp",
    "udp",
}


def check_private_network_firewall_rule_protocol(value: str) -> PrivateNetworkFirewallRuleProtocol:
    if value in PRIVATE_NETWORK_FIREWALL_RULE_PROTOCOL_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {PRIVATE_NETWORK_FIREWALL_RULE_PROTOCOL_VALUES!r}")
