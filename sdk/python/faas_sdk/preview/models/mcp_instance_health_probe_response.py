from __future__ import annotations

from collections.abc import Mapping
from typing import Any, TypeVar
from uuid import UUID

from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..types import UNSET, Unset

T = TypeVar("T", bound="MCPInstanceHealthProbeResponse")


@_attrs_define
class MCPInstanceHealthProbeResponse:
    """Bounded /healthz response from one exact replica with identity stamped by the platform."""

    app_id: UUID
    deployment_id: UUID
    instance_id: UUID
    http_status: int
    """HTTP status returned by the workload's /healthz handler."""
    health_body: str
    """UTF-8 body returned by the workload's /healthz handler."""
    health_body_too_large: bool | Unset = UNSET
    """True when the workload response exceeded the 64 KiB probe limit and health_body contains only its bounded
    prefix."""
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        app_id = str(self.app_id)

        deployment_id = str(self.deployment_id)

        instance_id = str(self.instance_id)

        http_status = self.http_status

        health_body = self.health_body

        health_body_too_large = self.health_body_too_large

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "app_id": app_id,
                "deployment_id": deployment_id,
                "instance_id": instance_id,
                "http_status": http_status,
                "health_body": health_body,
            }
        )
        if health_body_too_large is not UNSET:
            field_dict["health_body_too_large"] = health_body_too_large

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        app_id = UUID(d.pop("app_id"))

        deployment_id = UUID(d.pop("deployment_id"))

        instance_id = UUID(d.pop("instance_id"))

        http_status = d.pop("http_status")

        health_body = d.pop("health_body")

        health_body_too_large = d.pop("health_body_too_large", UNSET)

        mcp_instance_health_probe_response = cls(
            app_id=app_id,
            deployment_id=deployment_id,
            instance_id=instance_id,
            http_status=http_status,
            health_body=health_body,
            health_body_too_large=health_body_too_large,
        )

        mcp_instance_health_probe_response.additional_properties = d
        return mcp_instance_health_probe_response

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
