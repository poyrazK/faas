from __future__ import annotations

import datetime
from collections.abc import Mapping
from typing import Any, TypeVar

from attrs import define as _attrs_define
from attrs import field as _attrs_field

T = TypeVar("T", bound="RotateAlertRuleSecretResponse")


@_attrs_define
class RotateAlertRuleSecretResponse:
    """rotate-secret response — returns the rotated_at timestamp + the masked constant."""

    rotated_at: datetime.datetime
    webhook_secret_sealed_masked: str
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        rotated_at = self.rotated_at.isoformat()

        webhook_secret_sealed_masked = self.webhook_secret_sealed_masked

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "rotated_at": rotated_at,
                "webhook_secret_sealed_masked": webhook_secret_sealed_masked,
            }
        )

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        rotated_at = datetime.datetime.fromisoformat(d.pop("rotated_at"))

        webhook_secret_sealed_masked = d.pop("webhook_secret_sealed_masked")

        rotate_alert_rule_secret_response = cls(
            rotated_at=rotated_at,
            webhook_secret_sealed_masked=webhook_secret_sealed_masked,
        )

        rotate_alert_rule_secret_response.additional_properties = d
        return rotate_alert_rule_secret_response

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
