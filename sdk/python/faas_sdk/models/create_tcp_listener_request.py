from __future__ import annotations

from collections.abc import Mapping
from typing import Any, TypeVar

from attrs import define as _attrs_define

from ..types import UNSET, Unset

T = TypeVar("T", bound="CreateTCPListenerRequest")


@_attrs_define
class CreateTCPListenerRequest:
    """Request to expose one workload TCP port."""

    name: str
    guest_port: int
    public_port: int | Unset = UNSET
    """Optional stable public port; Gregale allocates one when omitted."""

    def to_dict(self) -> dict[str, Any]:
        name = self.name

        guest_port = self.guest_port

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

        public_port = d.pop("public_port", UNSET)

        create_tcp_listener_request = cls(
            name=name,
            guest_port=guest_port,
            public_port=public_port,
        )

        return create_tcp_listener_request
