from __future__ import annotations

from collections.abc import Mapping
from typing import Any, TypeVar

from attrs import define as _attrs_define
from attrs import field as _attrs_field

T = TypeVar("T", bound="RotateAppWebhookSecretRequest")


@_attrs_define
class RotateAppWebhookSecretRequest:
    """Caller-supplied replacement signing secret. The value is sealed at
    rest and never returned in a response.

        Example:
            {'webhook_secret': 'replacement-secret-from-manager'}

    """

    webhook_secret: str
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        webhook_secret = self.webhook_secret

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "webhook_secret": webhook_secret,
            }
        )

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        webhook_secret = d.pop("webhook_secret")

        rotate_app_webhook_secret_request = cls(
            webhook_secret=webhook_secret,
        )

        rotate_app_webhook_secret_request.additional_properties = d
        return rotate_app_webhook_secret_request

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
