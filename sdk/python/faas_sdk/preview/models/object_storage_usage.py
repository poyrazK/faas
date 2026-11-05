from __future__ import annotations

import datetime
from collections.abc import Mapping
from typing import Any, TypeVar

from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..models.object_storage_usage_unavailable_meters_item import (
    ObjectStorageUsageUnavailableMetersItem,
    check_object_storage_usage_unavailable_meters_item,
)
from ..types import UNSET, Unset

T = TypeVar("T", bound="ObjectStorageUsage")


@_attrs_define
class ObjectStorageUsage:
    """Account-wide usage and reserved capacity. In gateway_safety_v1 requests count durable provider attempts and egress
    counts full response lengths reserved before forwarding; disconnects retain reservations. Fields named in
    unavailable_meters are unknown, their numeric placeholders are not measured zeros and must not be billed. Legacy
    costs are EUR millicents, not a customer invoice.

    """

    observed_bytes: int
    capacity_bytes: int
    capacity_keys: int
    stored_byte_hours: int
    request_count: int
    egress_bytes: int
    cost_millicents: int
    authorizations: int
    fresh: bool
    period_start: datetime.datetime
    unavailable_meters: list[ObjectStorageUsageUnavailableMetersItem] | Unset = UNSET
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        observed_bytes = self.observed_bytes

        capacity_bytes = self.capacity_bytes

        capacity_keys = self.capacity_keys

        stored_byte_hours = self.stored_byte_hours

        request_count = self.request_count

        egress_bytes = self.egress_bytes

        cost_millicents = self.cost_millicents

        authorizations = self.authorizations

        fresh = self.fresh

        period_start = self.period_start.isoformat()

        unavailable_meters: list[str] | Unset = UNSET
        if not isinstance(self.unavailable_meters, Unset):
            unavailable_meters = []
            for unavailable_meters_item_data in self.unavailable_meters:
                unavailable_meters_item: str = unavailable_meters_item_data
                unavailable_meters.append(unavailable_meters_item)

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "observed_bytes": observed_bytes,
                "capacity_bytes": capacity_bytes,
                "capacity_keys": capacity_keys,
                "stored_byte_hours": stored_byte_hours,
                "request_count": request_count,
                "egress_bytes": egress_bytes,
                "cost_millicents": cost_millicents,
                "authorizations": authorizations,
                "fresh": fresh,
                "period_start": period_start,
            }
        )
        if unavailable_meters is not UNSET:
            field_dict["unavailable_meters"] = unavailable_meters

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        observed_bytes = d.pop("observed_bytes")

        capacity_bytes = d.pop("capacity_bytes")

        capacity_keys = d.pop("capacity_keys")

        stored_byte_hours = d.pop("stored_byte_hours")

        request_count = d.pop("request_count")

        egress_bytes = d.pop("egress_bytes")

        cost_millicents = d.pop("cost_millicents")

        authorizations = d.pop("authorizations")

        fresh = d.pop("fresh")

        period_start = datetime.datetime.fromisoformat(d.pop("period_start"))

        _unavailable_meters = d.pop("unavailable_meters", UNSET)
        unavailable_meters: list[ObjectStorageUsageUnavailableMetersItem] | Unset = UNSET
        if _unavailable_meters is not UNSET:
            unavailable_meters = []
            for unavailable_meters_item_data in _unavailable_meters:
                unavailable_meters_item = check_object_storage_usage_unavailable_meters_item(
                    unavailable_meters_item_data
                )

                unavailable_meters.append(unavailable_meters_item)

        object_storage_usage = cls(
            observed_bytes=observed_bytes,
            capacity_bytes=capacity_bytes,
            capacity_keys=capacity_keys,
            stored_byte_hours=stored_byte_hours,
            request_count=request_count,
            egress_bytes=egress_bytes,
            cost_millicents=cost_millicents,
            authorizations=authorizations,
            fresh=fresh,
            period_start=period_start,
            unavailable_meters=unavailable_meters,
        )

        object_storage_usage.additional_properties = d
        return object_storage_usage

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
