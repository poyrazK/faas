from __future__ import annotations

import datetime
from collections.abc import Mapping
from typing import Any, TypeVar
from uuid import UUID

from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..types import UNSET, Unset

T = TypeVar("T", bound="DebugRunningRequestAttribution")


@_attrs_define
class DebugRunningRequestAttribution:
    """The nearest retained request-telemetry representative linked to a
    request-activity cause. A representative may contain several
    collapsed requests; match_delta_ms and count make that limitation
    explicit.

    """

    telemetry_id: UUID
    deployment_id: UUID
    route: str
    method: str
    received_at: datetime.datetime
    count: int
    match_delta_ms: int
    trace_id: str | Unset = UNSET
    wake_id: str | Unset = UNSET
    instance_id: str | Unset = UNSET
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        telemetry_id = str(self.telemetry_id)

        deployment_id = str(self.deployment_id)

        route = self.route

        method = self.method

        received_at = self.received_at.isoformat()

        count = self.count

        match_delta_ms = self.match_delta_ms

        trace_id = self.trace_id

        wake_id = self.wake_id

        instance_id = self.instance_id

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "telemetry_id": telemetry_id,
                "deployment_id": deployment_id,
                "route": route,
                "method": method,
                "received_at": received_at,
                "count": count,
                "match_delta_ms": match_delta_ms,
            }
        )
        if trace_id is not UNSET:
            field_dict["trace_id"] = trace_id
        if wake_id is not UNSET:
            field_dict["wake_id"] = wake_id
        if instance_id is not UNSET:
            field_dict["instance_id"] = instance_id

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        telemetry_id = UUID(d.pop("telemetry_id"))

        deployment_id = UUID(d.pop("deployment_id"))

        route = d.pop("route")

        method = d.pop("method")

        received_at = datetime.datetime.fromisoformat(d.pop("received_at"))

        count = d.pop("count")

        match_delta_ms = d.pop("match_delta_ms")

        trace_id = d.pop("trace_id", UNSET)

        wake_id = d.pop("wake_id", UNSET)

        instance_id = d.pop("instance_id", UNSET)

        debug_running_request_attribution = cls(
            telemetry_id=telemetry_id,
            deployment_id=deployment_id,
            route=route,
            method=method,
            received_at=received_at,
            count=count,
            match_delta_ms=match_delta_ms,
            trace_id=trace_id,
            wake_id=wake_id,
            instance_id=instance_id,
        )

        debug_running_request_attribution.additional_properties = d
        return debug_running_request_attribution

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
