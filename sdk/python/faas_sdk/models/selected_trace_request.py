from __future__ import annotations

import datetime
from collections.abc import Mapping
from typing import Any, TypeVar
from uuid import UUID

from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..types import UNSET, Unset

T = TypeVar("T", bound="SelectedTraceRequest")


@_attrs_define
class SelectedTraceRequest:
    """Retained metadata only; no spans, headers, payloads or infrastructure fields."""

    app_id: UUID
    id: UUID
    route: str
    method: str
    status: int
    latency_ms: int
    count: int
    cold_boot: bool
    received_at: datetime.datetime
    deployment_id: UUID | Unset = UNSET
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        app_id = str(self.app_id)

        id = str(self.id)

        route = self.route

        method = self.method

        status = self.status

        latency_ms = self.latency_ms

        count = self.count

        cold_boot = self.cold_boot

        received_at = self.received_at.isoformat()

        deployment_id: str | Unset = UNSET
        if not isinstance(self.deployment_id, Unset):
            deployment_id = str(self.deployment_id)

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "app_id": app_id,
                "id": id,
                "route": route,
                "method": method,
                "status": status,
                "latency_ms": latency_ms,
                "count": count,
                "cold_boot": cold_boot,
                "received_at": received_at,
            }
        )
        if deployment_id is not UNSET:
            field_dict["deployment_id"] = deployment_id

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        app_id = UUID(d.pop("app_id"))

        id = UUID(d.pop("id"))

        route = d.pop("route")

        method = d.pop("method")

        status = d.pop("status")

        latency_ms = d.pop("latency_ms")

        count = d.pop("count")

        cold_boot = d.pop("cold_boot")

        received_at = datetime.datetime.fromisoformat(d.pop("received_at"))

        _deployment_id = d.pop("deployment_id", UNSET)
        deployment_id: UUID | Unset
        if isinstance(_deployment_id, Unset):
            deployment_id = UNSET
        else:
            deployment_id = UUID(_deployment_id)

        selected_trace_request = cls(
            app_id=app_id,
            id=id,
            route=route,
            method=method,
            status=status,
            latency_ms=latency_ms,
            count=count,
            cold_boot=cold_boot,
            received_at=received_at,
            deployment_id=deployment_id,
        )

        selected_trace_request.additional_properties = d
        return selected_trace_request

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
