from __future__ import annotations

from collections.abc import Mapping
from typing import Any, TypeVar, cast

from attrs import define as _attrs_define
from attrs import field as _attrs_field

T = TypeVar("T", bound="UpdatePrivateNetworkPolicyRequest")


@_attrs_define
class UpdatePrivateNetworkPolicyRequest:
    """PUT /v1/networks/{id}/policy body."""

    allowed_cidrs: list[str]
    """CIDRs reachable by attached workloads; empty disables the policy."""
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        allowed_cidrs = self.allowed_cidrs

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "allowed_cidrs": allowed_cidrs,
            }
        )

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        allowed_cidrs = cast(list[str], d.pop("allowed_cidrs"))

        update_private_network_policy_request = cls(
            allowed_cidrs=allowed_cidrs,
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
