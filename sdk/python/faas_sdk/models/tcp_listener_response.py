from __future__ import annotations

import datetime
from collections.abc import Mapping
from typing import Any, TypeVar

from attrs import define as _attrs_define

from ..models.tcp_listener_response_protocol import TCPListenerResponseProtocol, check_tcp_listener_response_protocol

T = TypeVar("T", bound="TCPListenerResponse")


@_attrs_define
class TCPListenerResponse:
    """One app-owned raw TCP listener with a stable public endpoint."""

    id: str
    """Stable listener identifier."""
    name: str
    guest_port: int
    """TCP port exposed by the workload."""
    public_port: int
    """Stable Gregale public TCP port."""
    protocol: TCPListenerResponseProtocol
    enabled: bool
    """Whether the edge accepts new TCP connections."""
    created_at: datetime.datetime
    updated_at: datetime.datetime

    def to_dict(self) -> dict[str, Any]:
        id = self.id

        name = self.name

        guest_port = self.guest_port

        public_port = self.public_port

        protocol: str = self.protocol

        enabled = self.enabled

        created_at = self.created_at.isoformat()

        updated_at = self.updated_at.isoformat()

        field_dict: dict[str, Any] = {}

        field_dict.update(
            {
                "id": id,
                "name": name,
                "guest_port": guest_port,
                "public_port": public_port,
                "protocol": protocol,
                "enabled": enabled,
                "created_at": created_at,
                "updated_at": updated_at,
            }
        )

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        id = d.pop("id")

        name = d.pop("name")

        guest_port = d.pop("guest_port")

        public_port = d.pop("public_port")

        protocol = check_tcp_listener_response_protocol(d.pop("protocol"))

        enabled = d.pop("enabled")

        created_at = datetime.datetime.fromisoformat(d.pop("created_at"))

        updated_at = datetime.datetime.fromisoformat(d.pop("updated_at"))

        tcp_listener_response = cls(
            id=id,
            name=name,
            guest_port=guest_port,
            public_port=public_port,
            protocol=protocol,
            enabled=enabled,
            created_at=created_at,
            updated_at=updated_at,
        )

        return tcp_listener_response
