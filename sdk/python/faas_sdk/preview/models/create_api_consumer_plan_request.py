from __future__ import annotations

from collections.abc import Mapping
from typing import TYPE_CHECKING, Any, TypeVar, cast

from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..types import UNSET, Unset

if TYPE_CHECKING:
    from ..models.create_api_consumer_plan_request_route_limits import CreateAPIConsumerPlanRequestRouteLimits


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
    """Percentages of max_units_per_month at which a consumer.usage_threshold webhook fires, once per consumer per
    UTC month. Requires max_units_per_month."""
    route_limits: CreateAPIConsumerPlanRequestRouteLimits | Unset = UNSET
    """Per-consumer caps on single billing routes, keyed by route label: an HTTP method and path template, or "MCP
    /tools/<name>" for one MCP tool. At most 50."""
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        name = self.name

        max_requests_per_minute = self.max_requests_per_minute

        max_units_per_month = self.max_units_per_month

        alert_thresholds_percent: list[int] | Unset = UNSET
        if not isinstance(self.alert_thresholds_percent, Unset):
            alert_thresholds_percent = self.alert_thresholds_percent

        route_limits: dict[str, Any] | Unset = UNSET
        if not isinstance(self.route_limits, Unset):
            route_limits = self.route_limits.to_dict()

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
        if route_limits is not UNSET:
            field_dict["route_limits"] = route_limits

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        from ..models.create_api_consumer_plan_request_route_limits import CreateAPIConsumerPlanRequestRouteLimits

        d = dict(src_dict)
        name = d.pop("name")

        max_requests_per_minute = d.pop("max_requests_per_minute", UNSET)

        max_units_per_month = d.pop("max_units_per_month", UNSET)

        alert_thresholds_percent = cast(list[int], d.pop("alert_thresholds_percent", UNSET))

        _route_limits = d.pop("route_limits", UNSET)
        route_limits: CreateAPIConsumerPlanRequestRouteLimits | Unset
        if isinstance(_route_limits, Unset):
            route_limits = UNSET
        else:
            route_limits = CreateAPIConsumerPlanRequestRouteLimits.from_dict(_route_limits)

        create_api_consumer_plan_request = cls(
            name=name,
            max_requests_per_minute=max_requests_per_minute,
            max_units_per_month=max_units_per_month,
            alert_thresholds_percent=alert_thresholds_percent,
            route_limits=route_limits,
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
