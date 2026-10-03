from __future__ import annotations

import datetime
from collections.abc import Mapping
from typing import Any, TypeVar
from uuid import UUID

from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..models.route_monitor_webhook_payload_status import (
    RouteMonitorWebhookPayloadStatus,
    check_route_monitor_webhook_payload_status,
)

T = TypeVar("T", bound="RouteMonitorWebhookPayload")


@_attrs_define
class RouteMonitorWebhookPayload:
    """Metadata-only transition with an authenticated saved incident path. Excludes route inventory, counts, customer IDs
    and request data.

    """

    version: int
    app_id: UUID
    deployment_id: UUID
    incident_id: UUID
    revision: int
    status: RouteMonitorWebhookPayloadStatus
    checked_at: datetime.datetime
    incident_path: str
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        version = self.version

        app_id = str(self.app_id)

        deployment_id = str(self.deployment_id)

        incident_id = str(self.incident_id)

        revision = self.revision

        status: str = self.status

        checked_at = self.checked_at.isoformat()

        incident_path = self.incident_path

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "version": version,
                "app_id": app_id,
                "deployment_id": deployment_id,
                "incident_id": incident_id,
                "revision": revision,
                "status": status,
                "checked_at": checked_at,
                "incident_path": incident_path,
            }
        )

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        version = d.pop("version")

        app_id = UUID(d.pop("app_id"))

        deployment_id = UUID(d.pop("deployment_id"))

        incident_id = UUID(d.pop("incident_id"))

        revision = d.pop("revision")

        status = check_route_monitor_webhook_payload_status(d.pop("status"))

        checked_at = datetime.datetime.fromisoformat(d.pop("checked_at"))

        incident_path = d.pop("incident_path")

        route_monitor_webhook_payload = cls(
            version=version,
            app_id=app_id,
            deployment_id=deployment_id,
            incident_id=incident_id,
            revision=revision,
            status=status,
            checked_at=checked_at,
            incident_path=incident_path,
        )

        route_monitor_webhook_payload.additional_properties = d
        return route_monitor_webhook_payload

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
