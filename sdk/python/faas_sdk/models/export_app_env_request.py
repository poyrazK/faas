from __future__ import annotations

from collections.abc import Mapping
from typing import Any, TypeVar

from attrs import define as _attrs_define
from attrs import field as _attrs_field

T = TypeVar("T", bound="ExportAppEnvRequest")


@_attrs_define
class ExportAppEnvRequest:
    """Explicit acknowledgement required before downloading potentially sensitive plaintext env values."""

    acknowledge_sensitive_values: bool
    """Explicit acknowledgement that plaintext downloaded values may be sensitive."""
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        acknowledge_sensitive_values = self.acknowledge_sensitive_values

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "acknowledge_sensitive_values": acknowledge_sensitive_values,
            }
        )

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        acknowledge_sensitive_values = d.pop("acknowledge_sensitive_values")

        export_app_env_request = cls(
            acknowledge_sensitive_values=acknowledge_sensitive_values,
        )

        export_app_env_request.additional_properties = d
        return export_app_env_request

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
