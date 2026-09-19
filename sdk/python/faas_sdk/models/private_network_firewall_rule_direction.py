from typing import Literal

PrivateNetworkFirewallRuleDirection = Literal["egress", "ingress"]

PRIVATE_NETWORK_FIREWALL_RULE_DIRECTION_VALUES: set[PrivateNetworkFirewallRuleDirection] = {
    "egress",
    "ingress",
}


def check_private_network_firewall_rule_direction(value: str) -> PrivateNetworkFirewallRuleDirection:
    if value in PRIVATE_NETWORK_FIREWALL_RULE_DIRECTION_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {PRIVATE_NETWORK_FIREWALL_RULE_DIRECTION_VALUES!r}")
