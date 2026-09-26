from __future__ import annotations

from collections.abc import Mapping
from typing import TYPE_CHECKING, Any, TypeVar
from uuid import UUID

from attrs import define as _attrs_define
from attrs import field as _attrs_field

if TYPE_CHECKING:
    from ..models.debug_telemetry_request_item import DebugTelemetryRequestItem


T = TypeVar("T", bound="PlatformTenantActivityItem")


@_attrs_define
class PlatformTenantActivityItem:
    """One observed telemetry row and the app it belongs to. Request details contain no HTTP body or headers."""

    app_id: UUID
    request: DebugTelemetryRequestItem
    """One bounded latency-bucket row representing gateway-served requests, persisted by the recorder/publisher."""
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        app_id = str(self.app_id)

        request = self.request.to_dict()

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "app_id": app_id,
                "request": request,
            }
        )

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        from ..models.debug_telemetry_request_item import DebugTelemetryRequestItem

        d = dict(src_dict)
        app_id = UUID(d.pop("app_id"))

        request = DebugTelemetryRequestItem.from_dict(d.pop("request"))

        platform_tenant_activity_item = cls(
            app_id=app_id,
            request=request,
        )

        platform_tenant_activity_item.additional_properties = d
        return platform_tenant_activity_item

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
