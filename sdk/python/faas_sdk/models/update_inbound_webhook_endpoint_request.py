from __future__ import annotations

from collections.abc import Mapping
from typing import Any, TypeVar

from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..types import UNSET, Unset

T = TypeVar("T", bound="UpdateInboundWebhookEndpointRequest")


@_attrs_define
class UpdateInboundWebhookEndpointRequest:
    """Omitted fields remain unchanged. A signing_secret value rotates the provider secret in place."""

    signing_secret: str | Unset = UNSET
    delivery_path: str | Unset = UNSET
    enabled: bool | Unset = UNSET
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        signing_secret = self.signing_secret

        delivery_path = self.delivery_path

        enabled = self.enabled

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update({})
        if signing_secret is not UNSET:
            field_dict["signing_secret"] = signing_secret
        if delivery_path is not UNSET:
            field_dict["delivery_path"] = delivery_path
        if enabled is not UNSET:
            field_dict["enabled"] = enabled

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        signing_secret = d.pop("signing_secret", UNSET)

        delivery_path = d.pop("delivery_path", UNSET)

        enabled = d.pop("enabled", UNSET)

        update_inbound_webhook_endpoint_request = cls(
            signing_secret=signing_secret,
            delivery_path=delivery_path,
            enabled=enabled,
        )

        update_inbound_webhook_endpoint_request.additional_properties = d
        return update_inbound_webhook_endpoint_request

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
