from __future__ import annotations

from collections.abc import Mapping
from typing import Any, TypeVar

from attrs import define as _attrs_define
from attrs import field as _attrs_field

T = TypeVar("T", bound="CreateOutboundCredentialKeyRequest")


@_attrs_define
class CreateOutboundCredentialKeyRequest:
    """An extra provider key to seal for the outbound gateway, with a DNS-label label."""

    label: str
    authorization: str
    """Same format as the integration's primary credential; sealed for the outbound gateway and never returned."""
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        label = self.label

        authorization = self.authorization

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "label": label,
                "authorization": authorization,
            }
        )

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        label = d.pop("label")

        authorization = d.pop("authorization")

        create_outbound_credential_key_request = cls(
            label=label,
            authorization=authorization,
        )

        create_outbound_credential_key_request.additional_properties = d
        return create_outbound_credential_key_request

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
