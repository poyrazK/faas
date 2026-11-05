from __future__ import annotations

from collections.abc import Mapping
from typing import Any, TypeVar

from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..models.deployment_live_webhook_payload_status import (
    DeploymentLiveWebhookPayloadStatus,
    check_deployment_live_webhook_payload_status,
)

T = TypeVar("T", bound="DeploymentLiveWebhookPayload")


@_attrs_define
class DeploymentLiveWebhookPayload:
    """Deployment reached the live state. Delivery IDs are stable across retries."""

    app_id: str
    """App that owns this live deployment."""
    deployment_id: str
    """Deployment that entered the live state."""
    status: DeploymentLiveWebhookPayloadStatus
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        app_id = self.app_id

        deployment_id = self.deployment_id

        status: str = self.status

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "app_id": app_id,
                "deployment_id": deployment_id,
                "status": status,
            }
        )

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        app_id = d.pop("app_id")

        deployment_id = d.pop("deployment_id")

        status = check_deployment_live_webhook_payload_status(d.pop("status"))

        deployment_live_webhook_payload = cls(
            app_id=app_id,
            deployment_id=deployment_id,
            status=status,
        )

        deployment_live_webhook_payload.additional_properties = d
        return deployment_live_webhook_payload

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
