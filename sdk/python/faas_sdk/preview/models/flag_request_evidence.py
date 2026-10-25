from __future__ import annotations

import datetime
from collections.abc import Mapping
from typing import TYPE_CHECKING, Any, TypeVar
from uuid import UUID

from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..types import UNSET, Unset

if TYPE_CHECKING:
    from ..models.flag_evidence import FlagEvidence


T = TypeVar("T", bound="FlagRequestEvidence")


@_attrs_define
class FlagRequestEvidence:
    """Retained request aggregate containing bounded flag decisions."""

    id: UUID
    app_id: UUID
    deployment_id: UUID
    received_at: datetime.datetime
    route: str
    method: str
    status: int
    latency_ms: int
    count: int
    cold_boot: bool
    flags: list[FlagEvidence]
    customer_id: UUID | Unset = UNSET
    trace_id: str | Unset = UNSET
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        id = str(self.id)

        app_id = str(self.app_id)

        deployment_id = str(self.deployment_id)

        received_at = self.received_at.isoformat()

        route = self.route

        method = self.method

        status = self.status

        latency_ms = self.latency_ms

        count = self.count

        cold_boot = self.cold_boot

        flags = []
        for flags_item_data in self.flags:
            flags_item = flags_item_data.to_dict()
            flags.append(flags_item)

        customer_id: str | Unset = UNSET
        if not isinstance(self.customer_id, Unset):
            customer_id = str(self.customer_id)

        trace_id = self.trace_id

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "id": id,
                "app_id": app_id,
                "deployment_id": deployment_id,
                "received_at": received_at,
                "route": route,
                "method": method,
                "status": status,
                "latency_ms": latency_ms,
                "count": count,
                "cold_boot": cold_boot,
                "flags": flags,
            }
        )
        if customer_id is not UNSET:
            field_dict["customer_id"] = customer_id
        if trace_id is not UNSET:
            field_dict["trace_id"] = trace_id

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        from ..models.flag_evidence import FlagEvidence

        d = dict(src_dict)
        id = UUID(d.pop("id"))

        app_id = UUID(d.pop("app_id"))

        deployment_id = UUID(d.pop("deployment_id"))

        received_at = datetime.datetime.fromisoformat(d.pop("received_at"))

        route = d.pop("route")

        method = d.pop("method")

        status = d.pop("status")

        latency_ms = d.pop("latency_ms")

        count = d.pop("count")

        cold_boot = d.pop("cold_boot")

        flags = []
        _flags = d.pop("flags")
        for flags_item_data in _flags:
            flags_item = FlagEvidence.from_dict(flags_item_data)

            flags.append(flags_item)

        _customer_id = d.pop("customer_id", UNSET)
        customer_id: UUID | Unset
        if isinstance(_customer_id, Unset):
            customer_id = UNSET
        else:
            customer_id = UUID(_customer_id)

        trace_id = d.pop("trace_id", UNSET)

        flag_request_evidence = cls(
            id=id,
            app_id=app_id,
            deployment_id=deployment_id,
            received_at=received_at,
            route=route,
            method=method,
            status=status,
            latency_ms=latency_ms,
            count=count,
            cold_boot=cold_boot,
            flags=flags,
            customer_id=customer_id,
            trace_id=trace_id,
        )

        flag_request_evidence.additional_properties = d
        return flag_request_evidence

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
