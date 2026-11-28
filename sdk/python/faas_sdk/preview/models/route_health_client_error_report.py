from __future__ import annotations

from collections.abc import Mapping
from typing import TYPE_CHECKING, Any, TypeVar

from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..models.route_health_client_error_report_status import (
    RouteHealthClientErrorReportStatus,
    check_route_health_client_error_report_status,
)

if TYPE_CHECKING:
    from ..models.route_health_client_error_finding import RouteHealthClientErrorFinding


T = TypeVar("T", bound="RouteHealthClientErrorReport")


@_attrs_define
class RouteHealthClientErrorReport:
    """Live advisory watched-code evidence, independent of the 5xx/latency verdict. Each code uses at least 20 represented
    requests on both deployments per window, two matching regression windows, at least two candidate responses, a rate
    of at least 5 percent, three times stable, and at least five percentage points above stable. Stable expected
    rejection rates remain healthy. Sparse, one-sided, missing or pre-anchor evidence is unknown. Coverage inherits
    observed_only from the containing report; these observations neither prove a defect nor certify an SLO. Excluded
    from saved decisions, recovery and webhook payloads.

    """

    status: RouteHealthClientErrorReportStatus
    reason: str
    minimum_requests: int
    minimum_responses: int
    rate_floor: float
    rate_delta: float
    rate_factor: float
    statuses: list[RouteHealthClientErrorFinding]
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        status: str = self.status

        reason = self.reason

        minimum_requests = self.minimum_requests

        minimum_responses = self.minimum_responses

        rate_floor = self.rate_floor

        rate_delta = self.rate_delta

        rate_factor = self.rate_factor

        statuses = []
        for statuses_item_data in self.statuses:
            statuses_item = statuses_item_data.to_dict()
            statuses.append(statuses_item)

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "status": status,
                "reason": reason,
                "minimum_requests": minimum_requests,
                "minimum_responses": minimum_responses,
                "rate_floor": rate_floor,
                "rate_delta": rate_delta,
                "rate_factor": rate_factor,
                "statuses": statuses,
            }
        )

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        from ..models.route_health_client_error_finding import RouteHealthClientErrorFinding

        d = dict(src_dict)
        status = check_route_health_client_error_report_status(d.pop("status"))

        reason = d.pop("reason")

        minimum_requests = d.pop("minimum_requests")

        minimum_responses = d.pop("minimum_responses")

        rate_floor = d.pop("rate_floor")

        rate_delta = d.pop("rate_delta")

        rate_factor = d.pop("rate_factor")

        statuses = []
        _statuses = d.pop("statuses")
        for statuses_item_data in _statuses:
            statuses_item = RouteHealthClientErrorFinding.from_dict(statuses_item_data)

            statuses.append(statuses_item)

        route_health_client_error_report = cls(
            status=status,
            reason=reason,
            minimum_requests=minimum_requests,
            minimum_responses=minimum_responses,
            rate_floor=rate_floor,
            rate_delta=rate_delta,
            rate_factor=rate_factor,
            statuses=statuses,
        )

        route_health_client_error_report.additional_properties = d
        return route_health_client_error_report

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
