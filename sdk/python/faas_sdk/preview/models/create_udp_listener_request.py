from __future__ import annotations

from collections.abc import Mapping
from typing import Any, TypeVar, cast

from attrs import define as _attrs_define

from ..models.create_udp_listener_request_public_port_type_0 import (
    CreateUDPListenerRequestPublicPortType0,
    check_create_udp_listener_request_public_port_type_0,
)
from ..types import UNSET, Unset

T = TypeVar("T", bound="CreateUDPListenerRequest")


@_attrs_define
class CreateUDPListenerRequest:
    """Reserve a disabled public datagram endpoint for a declared workload port."""

    name: str
    guest_port: int
    public_port: CreateUDPListenerRequestPublicPortType0 | int | Unset = UNSET
    """Omit or set to zero for automatic allocation; otherwise reserve a port in the public UDP range."""

    def to_dict(self) -> dict[str, Any]:
        name = self.name

        guest_port = self.guest_port

        public_port: int | Unset
        if isinstance(self.public_port, Unset):
            public_port = UNSET
        elif isinstance(self.public_port, int):
            public_port = self.public_port
        else:
            public_port = self.public_port

        field_dict: dict[str, Any] = {}

        field_dict.update(
            {
                "name": name,
                "guest_port": guest_port,
            }
        )
        if public_port is not UNSET:
            field_dict["public_port"] = public_port

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        name = d.pop("name")

        guest_port = d.pop("guest_port")

        def _parse_public_port(data: object) -> CreateUDPListenerRequestPublicPortType0 | int | Unset:
            if isinstance(data, Unset):
                return data
            try:
                if not isinstance(data, int):
                    raise TypeError()
                public_port_type_0 = check_create_udp_listener_request_public_port_type_0(data)

                return public_port_type_0
            except (TypeError, ValueError, AttributeError, KeyError):
                pass
            return cast(CreateUDPListenerRequestPublicPortType0 | int | Unset, data)

        public_port = _parse_public_port(d.pop("public_port", UNSET))

        create_udp_listener_request = cls(
            name=name,
            guest_port=guest_port,
            public_port=public_port,
        )

        return create_udp_listener_request
