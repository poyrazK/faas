from __future__ import annotations

from collections.abc import Mapping
from typing import Any, TypeVar

from attrs import define as _attrs_define

from ..models.workload_port_protocol import WorkloadPortProtocol, check_workload_port_protocol
from ..types import UNSET, Unset

T = TypeVar("T", bound="WorkloadPort")


@_attrs_define
class WorkloadPort:
    """One protocol-aware listener in a container workload (ADR-165)."""

    port: int
    protocol: WorkloadPortProtocol
    name: str | Unset = UNSET

    def to_dict(self) -> dict[str, Any]:
        port = self.port

        protocol: str = self.protocol

        name = self.name

        field_dict: dict[str, Any] = {}

        field_dict.update(
            {
                "port": port,
                "protocol": protocol,
            }
        )
        if name is not UNSET:
            field_dict["name"] = name

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        port = d.pop("port")

        protocol = check_workload_port_protocol(d.pop("protocol"))

        name = d.pop("name", UNSET)

        workload_port = cls(
            port=port,
            protocol=protocol,
            name=name,
        )

        return workload_port
