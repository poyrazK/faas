from __future__ import annotations

from collections.abc import Mapping
from typing import Any, TypeVar

from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..types import UNSET, Unset

T = TypeVar("T", bound="DebugRunningFlowSummary")


@_attrs_define
class DebugRunningFlowSummary:
    """A bounded endpoint-level flow summary observed for a running
    instance. The platform omits payloads, headers, and unbounded
    connection data.

    """

    instance_id: str | Unset = UNSET
    protocol: str | Unset = UNSET
    remote_ip: str | Unset = UNSET
    remote_port: int | Unset = UNSET
    state: str | Unset = UNSET
    direction: str | Unset = UNSET
    count: int | Unset = UNSET
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        instance_id = self.instance_id

        protocol = self.protocol

        remote_ip = self.remote_ip

        remote_port = self.remote_port

        state = self.state

        direction = self.direction

        count = self.count

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update({})
        if instance_id is not UNSET:
            field_dict["instance_id"] = instance_id
        if protocol is not UNSET:
            field_dict["protocol"] = protocol
        if remote_ip is not UNSET:
            field_dict["remote_ip"] = remote_ip
        if remote_port is not UNSET:
            field_dict["remote_port"] = remote_port
        if state is not UNSET:
            field_dict["state"] = state
        if direction is not UNSET:
            field_dict["direction"] = direction
        if count is not UNSET:
            field_dict["count"] = count

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        instance_id = d.pop("instance_id", UNSET)

        protocol = d.pop("protocol", UNSET)

        remote_ip = d.pop("remote_ip", UNSET)

        remote_port = d.pop("remote_port", UNSET)

        state = d.pop("state", UNSET)

        direction = d.pop("direction", UNSET)

        count = d.pop("count", UNSET)

        debug_running_flow_summary = cls(
            instance_id=instance_id,
            protocol=protocol,
            remote_ip=remote_ip,
            remote_port=remote_port,
            state=state,
            direction=direction,
            count=count,
        )

        debug_running_flow_summary.additional_properties = d
        return debug_running_flow_summary

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
