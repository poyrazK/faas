from __future__ import annotations

from collections.abc import Mapping
from typing import Any, TypeVar

from attrs import define as _attrs_define
from attrs import field as _attrs_field

T = TypeVar("T", bound="RunControlCapabilities")


@_attrs_define
class RunControlCapabilities:
    """Available budget, attribution and history controls for this account."""

    configuration_available: bool
    max_customers: int
    max_bindings: int
    history_page_size: int
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        configuration_available = self.configuration_available

        max_customers = self.max_customers

        max_bindings = self.max_bindings

        history_page_size = self.history_page_size

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "configuration_available": configuration_available,
                "max_customers": max_customers,
                "max_bindings": max_bindings,
                "history_page_size": history_page_size,
            }
        )

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        configuration_available = d.pop("configuration_available")

        max_customers = d.pop("max_customers")

        max_bindings = d.pop("max_bindings")

        history_page_size = d.pop("history_page_size")

        run_control_capabilities = cls(
            configuration_available=configuration_available,
            max_customers=max_customers,
            max_bindings=max_bindings,
            history_page_size=history_page_size,
        )

        run_control_capabilities.additional_properties = d
        return run_control_capabilities

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
