from __future__ import annotations

from collections.abc import Mapping
from typing import TYPE_CHECKING, Any, TypeVar, cast

from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..types import UNSET, Unset

if TYPE_CHECKING:
    from ..models.private_network_firewall_rule import PrivateNetworkFirewallRule


T = TypeVar("T", bound="CreatePrivateNetworkRequest")


@_attrs_define
class CreatePrivateNetworkRequest:
    """POST /v1/networks body for a Gregale-owned network."""

    name: str
    region: str
    cidr: str
    allowed_cidrs: list[str] | Unset = UNSET
    """Optional reusable CIDR allowlist contained by cidr."""
    firewall_rules: list[PrivateNetworkFirewallRule] | Unset = UNSET
    """Optional protocol/port allow rules contained by cidr."""
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        name = self.name

        region = self.region

        cidr = self.cidr

        allowed_cidrs: list[str] | Unset = UNSET
        if not isinstance(self.allowed_cidrs, Unset):
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
                "name": name,
                "region": region,
                "cidr": cidr,
            }
        )
        if allowed_cidrs is not UNSET:
            field_dict["allowed_cidrs"] = allowed_cidrs
        if firewall_rules is not UNSET:
            field_dict["firewall_rules"] = firewall_rules

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        from ..models.private_network_firewall_rule import PrivateNetworkFirewallRule

        d = dict(src_dict)
        name = d.pop("name")

        region = d.pop("region")

        cidr = d.pop("cidr")

        allowed_cidrs = cast(list[str], d.pop("allowed_cidrs", UNSET))

        _firewall_rules = d.pop("firewall_rules", UNSET)
        firewall_rules: list[PrivateNetworkFirewallRule] | Unset = UNSET
        if _firewall_rules is not UNSET:
            firewall_rules = []
            for firewall_rules_item_data in _firewall_rules:
                firewall_rules_item = PrivateNetworkFirewallRule.from_dict(firewall_rules_item_data)

                firewall_rules.append(firewall_rules_item)

        create_private_network_request = cls(
            name=name,
            region=region,
            cidr=cidr,
            allowed_cidrs=allowed_cidrs,
            firewall_rules=firewall_rules,
        )

        create_private_network_request.additional_properties = d
        return create_private_network_request

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
