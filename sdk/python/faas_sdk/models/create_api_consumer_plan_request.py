from __future__ import annotations

from collections.abc import Mapping
from typing import Any, TypeVar, cast

from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..types import UNSET, Unset

T = TypeVar("T", bound="CreateAPIConsumerPlanRequest")


@_attrs_define
class CreateAPIConsumerPlanRequest:
    """Named consumer plan. Zero limits are unlimited."""

    name: str
    max_requests_per_minute: int | Unset = UNSET
    """Admitted requests per consumer per minute; over the limit the gateway returns 429."""
    max_units_per_month: int | Unset = UNSET
    """Weighted units per consumer per UTC month; over the cap the gateway returns 429 until the month ends."""
    alert_thresholds_percent: list[int] | Unset = UNSET
    """Percentages of max_units_per_month at which a consumer.usage_threshold webhook fires, once per consumer per UTC
    month (ADR-849). Requires max_units_per_month."""
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        name = self.name

        max_requests_per_minute = self.max_requests_per_minute

        max_units_per_month = self.max_units_per_month

        alert_thresholds_percent: list[int] | Unset = UNSET
        if not isinstance(self.alert_thresholds_percent, Unset):
            alert_thresholds_percent = self.alert_thresholds_percent

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "name": name,
            }
        )
        if max_requests_per_minute is not UNSET:
            field_dict["max_requests_per_minute"] = max_requests_per_minute
        if max_units_per_month is not UNSET:
            field_dict["max_units_per_month"] = max_units_per_month
        if alert_thresholds_percent is not UNSET:
            field_dict["alert_thresholds_percent"] = alert_thresholds_percent

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        name = d.pop("name")

        max_requests_per_minute = d.pop("max_requests_per_minute", UNSET)

        max_units_per_month = d.pop("max_units_per_month", UNSET)

        alert_thresholds_percent = cast(list[int], d.pop("alert_thresholds_percent", UNSET))

        create_api_consumer_plan_request = cls(
            name=name,
            max_requests_per_minute=max_requests_per_minute,
            max_units_per_month=max_units_per_month,
            alert_thresholds_percent=alert_thresholds_percent,
        )

        create_api_consumer_plan_request.additional_properties = d
        return create_api_consumer_plan_request

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
