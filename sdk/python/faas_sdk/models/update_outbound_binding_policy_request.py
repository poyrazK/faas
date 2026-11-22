from __future__ import annotations

from collections.abc import Mapping
from typing import Any, TypeVar, cast

from attrs import define as _attrs_define
from attrs import field as _attrs_field

T = TypeVar("T", bound="UpdateOutboundBindingPolicyRequest")


@_attrs_define
class UpdateOutboundBindingPolicyRequest:
    """Explicit nonempty method and path subsets for a bound app."""

    allowed_methods: list[str]
    allowed_path_prefixes: list[str]
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        allowed_methods = self.allowed_methods

        allowed_path_prefixes = self.allowed_path_prefixes

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "allowed_methods": allowed_methods,
                "allowed_path_prefixes": allowed_path_prefixes,
            }
        )

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        allowed_methods = cast(list[str], d.pop("allowed_methods"))

        allowed_path_prefixes = cast(list[str], d.pop("allowed_path_prefixes"))

        update_outbound_binding_policy_request = cls(
            allowed_methods=allowed_methods,
            allowed_path_prefixes=allowed_path_prefixes,
        )

        update_outbound_binding_policy_request.additional_properties = d
        return update_outbound_binding_policy_request

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
