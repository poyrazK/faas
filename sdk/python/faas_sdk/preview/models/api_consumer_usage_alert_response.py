from __future__ import annotations

import datetime
from collections.abc import Mapping
from typing import Any, TypeVar
from uuid import UUID

from attrs import define as _attrs_define
from attrs import field as _attrs_field

T = TypeVar("T", bound="APIConsumerUsageAlertResponse")


@_attrs_define
class APIConsumerUsageAlertResponse:
    """One plan alert threshold a consumer crossed in one UTC month (ADR-849)."""

    id: UUID
    consumer_id: UUID
    plan_id: UUID
    month_start: datetime.datetime
    threshold_percent: int
    limit_units: int
    used_units: int
    """Weighted units used this month when the threshold was crossed."""
    crossed_at: datetime.datetime
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        id = str(self.id)

        consumer_id = str(self.consumer_id)

        plan_id = str(self.plan_id)

        month_start = self.month_start.isoformat()

        threshold_percent = self.threshold_percent

        limit_units = self.limit_units

        used_units = self.used_units

        crossed_at = self.crossed_at.isoformat()

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "id": id,
                "consumer_id": consumer_id,
                "plan_id": plan_id,
                "month_start": month_start,
                "threshold_percent": threshold_percent,
                "limit_units": limit_units,
                "used_units": used_units,
                "crossed_at": crossed_at,
            }
        )

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        id = UUID(d.pop("id"))

        consumer_id = UUID(d.pop("consumer_id"))

        plan_id = UUID(d.pop("plan_id"))

        month_start = datetime.datetime.fromisoformat(d.pop("month_start"))

        threshold_percent = d.pop("threshold_percent")

        limit_units = d.pop("limit_units")

        used_units = d.pop("used_units")

        crossed_at = datetime.datetime.fromisoformat(d.pop("crossed_at"))

        api_consumer_usage_alert_response = cls(
            id=id,
            consumer_id=consumer_id,
            plan_id=plan_id,
            month_start=month_start,
            threshold_percent=threshold_percent,
            limit_units=limit_units,
            used_units=used_units,
            crossed_at=crossed_at,
        )

        api_consumer_usage_alert_response.additional_properties = d
        return api_consumer_usage_alert_response

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
