from __future__ import annotations

import datetime
from collections.abc import Mapping
from typing import TYPE_CHECKING, Any, TypeVar

from attrs import define as _attrs_define
from attrs import field as _attrs_field

if TYPE_CHECKING:
    from ..models.realtime_endpoint_usage_hour import RealtimeEndpointUsageHour
    from ..models.realtime_usage_hour import RealtimeUsageHour


T = TypeVar("T", bound="RealtimeUsageResponse")


@_attrs_define
class RealtimeUsageResponse:
    """An account's realtime usage against its fair-use limits."""

    plan: str
    """The account's plan."""
    connection_limit: int
    """Concurrent connections the plan allows per account; zero when the plan has no realtime."""
    open_connections: int
    """Connections open now on nodes that reported in the last 30 seconds."""
    from_: datetime.datetime
    """Start of the first hour returned."""
    to: datetime.datetime
    """End of the last hour returned (exclusive)."""
    hours: list[RealtimeUsageHour]
    """Account usage per hour, oldest first."""
    endpoints: list[RealtimeEndpointUsageHour]
    """Usage per endpoint and hour, oldest first."""
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        plan = self.plan

        connection_limit = self.connection_limit

        open_connections = self.open_connections

        from_ = self.from_.isoformat()

        to = self.to.isoformat()

        hours = []
        for hours_item_data in self.hours:
            hours_item = hours_item_data.to_dict()
            hours.append(hours_item)

        endpoints = []
        for endpoints_item_data in self.endpoints:
            endpoints_item = endpoints_item_data.to_dict()
            endpoints.append(endpoints_item)

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "plan": plan,
                "connection_limit": connection_limit,
                "open_connections": open_connections,
                "from": from_,
                "to": to,
                "hours": hours,
                "endpoints": endpoints,
            }
        )

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        from ..models.realtime_endpoint_usage_hour import RealtimeEndpointUsageHour
        from ..models.realtime_usage_hour import RealtimeUsageHour

        d = dict(src_dict)
        plan = d.pop("plan")

        connection_limit = d.pop("connection_limit")

        open_connections = d.pop("open_connections")

        from_ = datetime.datetime.fromisoformat(d.pop("from"))

        to = datetime.datetime.fromisoformat(d.pop("to"))

        hours = []
        _hours = d.pop("hours")
        for hours_item_data in _hours:
            hours_item = RealtimeUsageHour.from_dict(hours_item_data)

            hours.append(hours_item)

        endpoints = []
        _endpoints = d.pop("endpoints")
        for endpoints_item_data in _endpoints:
            endpoints_item = RealtimeEndpointUsageHour.from_dict(endpoints_item_data)

            endpoints.append(endpoints_item)

        realtime_usage_response = cls(
            plan=plan,
            connection_limit=connection_limit,
            open_connections=open_connections,
            from_=from_,
            to=to,
            hours=hours,
            endpoints=endpoints,
        )

        realtime_usage_response.additional_properties = d
        return realtime_usage_response

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
