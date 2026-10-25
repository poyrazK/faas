from __future__ import annotations

from collections.abc import Mapping
from typing import TYPE_CHECKING, Any, TypeVar, cast

from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..types import UNSET, Unset

if TYPE_CHECKING:
    from ..models.private_network_firewall_rule import PrivateNetworkFirewallRule


T = TypeVar("T", bound="UpdatePrivateNetworkPolicyRequest")


@_attrs_define
class UpdatePrivateNetworkPolicyRequest:
    """PUT /v1/networks/{id}/policy body."""

    allowed_cidrs: list[str]
    """CIDRs reachable by attached workloads; empty disables the CIDR restriction."""
    firewall_rules: list[PrivateNetworkFirewallRule] | Unset = UNSET
    """Protocol/port allow rules contained by the network CIDR; empty disables the rule restriction."""
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        allowed_cidrs = self.allowed_cidrs

        firewall_rules: list[dict[str, Any]] | Unset = UNSET
        if not isinstance(self.firewall_rules, Unset):
            firewall_rules = []
            for firewall_rules_item_data in self.firewall_rules:
                firewall_rules_item = firewall_rules_item_data.to_dict()
                firewall_rules.append(firewall_rules_item)

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "allowed_cidrs": allowed_cidrs,
            }
        )
        if firewall_rules is not UNSET:
            field_dict["firewall_rules"] = firewall_rules

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        from ..models.private_network_firewall_rule import PrivateNetworkFirewallRule

        d = dict(src_dict)
        allowed_cidrs = cast(list[str], d.pop("allowed_cidrs"))

        _firewall_rules = d.pop("firewall_rules", UNSET)
        firewall_rules: list[PrivateNetworkFirewallRule] | Unset = UNSET
        if _firewall_rules is not UNSET:
            firewall_rules = []
            for firewall_rules_item_data in _firewall_rules:
                firewall_rules_item = PrivateNetworkFirewallRule.from_dict(firewall_rules_item_data)

                firewall_rules.append(firewall_rules_item)

        update_private_network_policy_request = cls(
            allowed_cidrs=allowed_cidrs,
            firewall_rules=firewall_rules,
        )

        update_private_network_policy_request.additional_properties = d
        return update_private_network_policy_request

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
