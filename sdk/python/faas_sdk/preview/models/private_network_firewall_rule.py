from __future__ import annotations

from collections.abc import Mapping
from typing import Any, TypeVar, cast

from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..models.private_network_firewall_rule_direction import (
    PrivateNetworkFirewallRuleDirection,
    check_private_network_firewall_rule_direction,
)
from ..models.private_network_firewall_rule_protocol import (
    PrivateNetworkFirewallRuleProtocol,
    check_private_network_firewall_rule_protocol,
)
from ..types import UNSET, Unset

T = TypeVar("T", bound="PrivateNetworkFirewallRule")


@_attrs_define
class PrivateNetworkFirewallRule:
    """Provider-neutral private-network allow rule."""

    direction: PrivateNetworkFirewallRuleDirection
    protocol: PrivateNetworkFirewallRuleProtocol
    cidrs: list[str] | Unset = UNSET
    """Source CIDRs for ingress or destination CIDRs for egress; empty means the network CIDR."""
    ports: list[str] | Unset = UNSET
    """TCP/UDP ports or inclusive ranges such as 443 or 8000-8080; omit for ICMP."""
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        direction: str = self.direction

        protocol: str = self.protocol

        cidrs: list[str] | Unset = UNSET
        if not isinstance(self.cidrs, Unset):
            cidrs = self.cidrs

        ports: list[str] | Unset = UNSET
        if not isinstance(self.ports, Unset):
            ports = self.ports

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "direction": direction,
                "protocol": protocol,
            }
        )
        if cidrs is not UNSET:
            field_dict["cidrs"] = cidrs
        if ports is not UNSET:
            field_dict["ports"] = ports

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        direction = check_private_network_firewall_rule_direction(d.pop("direction"))

        protocol = check_private_network_firewall_rule_protocol(d.pop("protocol"))

        cidrs = cast(list[str], d.pop("cidrs", UNSET))

        ports = cast(list[str], d.pop("ports", UNSET))

        private_network_firewall_rule = cls(
            direction=direction,
            protocol=protocol,
            cidrs=cidrs,
            ports=ports,
        )

        private_network_firewall_rule.additional_properties = d
        return private_network_firewall_rule

    @property
    def additional_keys(self) -> list[str]:
        return list(self.additional_properties.keys())

    def __getitem__(self, key: str) -> Any:
        return self.additional_properties[key]

    def __setitem__(self, key: str, value: Any) -> None:
        self.additional_properties[key] = value

    def __delitem__(self, key: str) -> None:
        del self.additional_properties[key]

    def __contains__(self, key: str) -> bool:
        return key in self.additional_properties
