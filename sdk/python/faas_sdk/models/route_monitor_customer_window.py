from __future__ import annotations

import datetime
from collections.abc import Mapping
from typing import Any, TypeVar

from attrs import define as _attrs_define
from attrs import field as _attrs_field

T = TypeVar("T", bound="RouteMonitorCustomerWindow")


@_attrs_define
class RouteMonitorCustomerWindow:
    """Request-time identity attribution totals for one monitored window. These are observed telemetry counts, not unique
    people or billing data.

    """

    start: datetime.datetime
    end: datetime.datetime
    identified_requests: int
    unattributed_requests: int
    unresolved_identity_requests: int
    other_customer_requests: int
    """Identified requests belonging to cohorts outside the displayed five."""
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        start = self.start.isoformat()

        end = self.end.isoformat()

        identified_requests = self.identified_requests

        unattributed_requests = self.unattributed_requests

        unresolved_identity_requests = self.unresolved_identity_requests

        other_customer_requests = self.other_customer_requests

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "start": start,
                "end": end,
                "identified_requests": identified_requests,
                "unattributed_requests": unattributed_requests,
                "unresolved_identity_requests": unresolved_identity_requests,
                "other_customer_requests": other_customer_requests,
            }
        )

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        start = datetime.datetime.fromisoformat(d.pop("start"))

        end = datetime.datetime.fromisoformat(d.pop("end"))

        identified_requests = d.pop("identified_requests")

        unattributed_requests = d.pop("unattributed_requests")

        unresolved_identity_requests = d.pop("unresolved_identity_requests")

        other_customer_requests = d.pop("other_customer_requests")

        route_monitor_customer_window = cls(
            start=start,
            end=end,
            identified_requests=identified_requests,
            unattributed_requests=unattributed_requests,
            unresolved_identity_requests=unresolved_identity_requests,
            other_customer_requests=other_customer_requests,
        )

        route_monitor_customer_window.additional_properties = d
        return route_monitor_customer_window

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
