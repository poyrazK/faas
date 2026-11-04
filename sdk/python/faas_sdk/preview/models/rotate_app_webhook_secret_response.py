from __future__ import annotations

import datetime
from collections.abc import Mapping
from typing import Any, TypeVar

from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..models.rotate_app_webhook_secret_response_webhook_secret_sealed_masked import (
    RotateAppWebhookSecretResponseWebhookSecretSealedMasked,
    check_rotate_app_webhook_secret_response_webhook_secret_sealed_masked,
)

T = TypeVar("T", bound="RotateAppWebhookSecretResponse")


@_attrs_define
class RotateAppWebhookSecretResponse:
    """Response from POST /v1/apps/{slug}/webhooks/{id}/rotate-secret.
    The plaintext secret is NEVER returned; the masked field is
    a constant sentinel so the dashboard renders the same shape
    across all secret-bearing rows (mirrors AlertRule rotate).

        Example:
            {'rotated_at': '2026-08-06T12:00:00Z', 'webhook_secret_sealed_masked': '***'}

    """

    rotated_at: datetime.datetime
    webhook_secret_sealed_masked: RotateAppWebhookSecretResponseWebhookSecretSealedMasked
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        rotated_at = self.rotated_at.isoformat()

        webhook_secret_sealed_masked: str = self.webhook_secret_sealed_masked

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

        webhook_secret_sealed_masked = check_rotate_app_webhook_secret_response_webhook_secret_sealed_masked(
            d.pop("webhook_secret_sealed_masked")
        )

        rotate_app_webhook_secret_response = cls(
            rotated_at=rotated_at,
            webhook_secret_sealed_masked=webhook_secret_sealed_masked,
        )

        rotate_app_webhook_secret_response.additional_properties = d
        return rotate_app_webhook_secret_response

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
