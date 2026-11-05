from __future__ import annotations

import datetime
from collections.abc import Mapping
from typing import Any, TypeVar, cast
from uuid import UUID

from attrs import define as _attrs_define
from attrs import field as _attrs_field

T = TypeVar("T", bound="APIConsumerPlanResponse")


@_attrs_define
class APIConsumerPlanResponse:
    """Named consumer plan with enforcement limits."""

    id: UUID
    app_id: UUID
    name: str
    max_requests_per_minute: int
    max_units_per_month: int
    alert_thresholds_percent: list[int]
    """The plan's usage alert thresholds, ascending; empty means no alerts."""
    created_at: datetime.datetime
    updated_at: datetime.datetime
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        id = str(self.id)

        app_id = str(self.app_id)

        name = self.name

        max_requests_per_minute = self.max_requests_per_minute

        max_units_per_month = self.max_units_per_month

        alert_thresholds_percent = self.alert_thresholds_percent

        created_at = self.created_at.isoformat()

        updated_at = self.updated_at.isoformat()

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "id": id,
                "app_id": app_id,
                "name": name,
                "max_requests_per_minute": max_requests_per_minute,
                "max_units_per_month": max_units_per_month,
                "alert_thresholds_percent": alert_thresholds_percent,
                "created_at": created_at,
                "updated_at": updated_at,
            }
        )

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        id = UUID(d.pop("id"))

        app_id = UUID(d.pop("app_id"))

        name = d.pop("name")

        max_requests_per_minute = d.pop("max_requests_per_minute")

        max_units_per_month = d.pop("max_units_per_month")

        alert_thresholds_percent = cast(list[int], d.pop("alert_thresholds_percent"))

        created_at = datetime.datetime.fromisoformat(d.pop("created_at"))

        updated_at = datetime.datetime.fromisoformat(d.pop("updated_at"))

        api_consumer_plan_response = cls(
            id=id,
            app_id=app_id,
            name=name,
            max_requests_per_minute=max_requests_per_minute,
            max_units_per_month=max_units_per_month,
            alert_thresholds_percent=alert_thresholds_percent,
            created_at=created_at,
            updated_at=updated_at,
        )

        api_consumer_plan_response.additional_properties = d
        return api_consumer_plan_response

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
