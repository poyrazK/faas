from __future__ import annotations

import datetime
from collections.abc import Mapping
from typing import TYPE_CHECKING, Any, TypeVar

from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..models.route_health_client_error_window_status import (
    RouteHealthClientErrorWindowStatus,
    check_route_health_client_error_window_status,
)

if TYPE_CHECKING:
    from ..models.route_health_status_counts import RouteHealthStatusCounts


T = TypeVar("T", bound="RouteHealthClientErrorWindow")


@_attrs_define
class RouteHealthClientErrorWindow:
    """Candidate/stable observations for one watched code within the shared aggregate observation window. Only deployment-
    attributed responses participate; pre-routing rejections without a deployment cannot be compared.

    """

    start: datetime.datetime
    end: datetime.datetime
    candidate: RouteHealthStatusCounts
    """Weighted requests and responses for one watched HTTP code in one deployment/window. Rate is responses
    divided by requests, zero when requests are zero."""
    stable: RouteHealthStatusCounts
    """Weighted requests and responses for one watched HTTP code in one deployment/window. Rate is responses
    divided by requests, zero when requests are zero."""
    status: RouteHealthClientErrorWindowStatus
    reason: str
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        start = self.start.isoformat()

        end = self.end.isoformat()

        candidate = self.candidate.to_dict()

        stable = self.stable.to_dict()

        status: str = self.status

        reason = self.reason

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "start": start,
                "end": end,
                "candidate": candidate,
                "stable": stable,
                "status": status,
                "reason": reason,
            }
        )

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        from ..models.route_health_status_counts import RouteHealthStatusCounts

        d = dict(src_dict)
        start = datetime.datetime.fromisoformat(d.pop("start"))

        end = datetime.datetime.fromisoformat(d.pop("end"))

        candidate = RouteHealthStatusCounts.from_dict(d.pop("candidate"))

        stable = RouteHealthStatusCounts.from_dict(d.pop("stable"))

        status = check_route_health_client_error_window_status(d.pop("status"))

        reason = d.pop("reason")

        route_health_client_error_window = cls(
            start=start,
            end=end,
            candidate=candidate,
            stable=stable,
            status=status,
            reason=reason,
        )

        route_health_client_error_window.additional_properties = d
        return route_health_client_error_window

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
