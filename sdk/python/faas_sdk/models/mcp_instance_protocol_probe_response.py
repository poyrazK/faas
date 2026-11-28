from __future__ import annotations

from collections.abc import Mapping
from typing import Any, TypeVar
from uuid import UUID

from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..types import UNSET, Unset

T = TypeVar("T", bound="MCPInstanceProtocolProbeResponse")


@_attrs_define
class MCPInstanceProtocolProbeResponse:
    """Bounded MCP response from one exact replica with platform-stamped identity."""

    app_id: UUID
    deployment_id: UUID
    instance_id: UUID
    http_status: int
    body: str
    content_type: str | Unset = UNSET
    protocol_version: str | Unset = UNSET
    session_id: str | Unset = UNSET
    session_id_present: bool | Unset = UNSET
    """True when the workload returned an MCP-Session-Id header, including when its value exceeded the response
    header cap."""
    body_too_large: bool | Unset = UNSET
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        app_id = str(self.app_id)

        deployment_id = str(self.deployment_id)

        instance_id = str(self.instance_id)

        http_status = self.http_status

        body = self.body

        content_type = self.content_type

        protocol_version = self.protocol_version

        session_id = self.session_id

        session_id_present = self.session_id_present

        body_too_large = self.body_too_large

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "app_id": app_id,
                "deployment_id": deployment_id,
                "instance_id": instance_id,
                "http_status": http_status,
                "body": body,
            }
        )
        if content_type is not UNSET:
            field_dict["content_type"] = content_type
        if protocol_version is not UNSET:
            field_dict["protocol_version"] = protocol_version
        if session_id is not UNSET:
            field_dict["session_id"] = session_id
        if session_id_present is not UNSET:
            field_dict["session_id_present"] = session_id_present
        if body_too_large is not UNSET:
            field_dict["body_too_large"] = body_too_large

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        app_id = UUID(d.pop("app_id"))

        deployment_id = UUID(d.pop("deployment_id"))

        instance_id = UUID(d.pop("instance_id"))

        http_status = d.pop("http_status")

        body = d.pop("body")

        content_type = d.pop("content_type", UNSET)

        protocol_version = d.pop("protocol_version", UNSET)

        session_id = d.pop("session_id", UNSET)

        session_id_present = d.pop("session_id_present", UNSET)

        body_too_large = d.pop("body_too_large", UNSET)

        mcp_instance_protocol_probe_response = cls(
            app_id=app_id,
            deployment_id=deployment_id,
            instance_id=instance_id,
            http_status=http_status,
            body=body,
            content_type=content_type,
            protocol_version=protocol_version,
            session_id=session_id,
            session_id_present=session_id_present,
            body_too_large=body_too_large,
        )

        mcp_instance_protocol_probe_response.additional_properties = d
        return mcp_instance_protocol_probe_response

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
