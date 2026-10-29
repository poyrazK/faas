from __future__ import annotations

from collections.abc import Mapping
from typing import Any, TypeVar

from attrs import define as _attrs_define
from attrs import field as _attrs_field

T = TypeVar("T", bound="DevBridgeCredentials")


@_attrs_define
class DevBridgeCredentials:
    """Distinct secrets for laptop attachment and request routing."""

    attachment_token: str
    request_token: str
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        attachment_token = self.attachment_token

        request_token = self.request_token

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "attachment_token": attachment_token,
                "request_token": request_token,
            }
        )

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        attachment_token = d.pop("attachment_token")

        request_token = d.pop("request_token")

        dev_bridge_credentials = cls(
            attachment_token=attachment_token,
            request_token=request_token,
        )

        dev_bridge_credentials.additional_properties = d
        return dev_bridge_credentials

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
