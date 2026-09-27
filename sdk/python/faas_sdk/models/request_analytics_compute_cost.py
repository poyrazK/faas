from __future__ import annotations

from collections.abc import Mapping
from typing import Any, TypeVar

from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..models.request_analytics_compute_cost_allocation_method import (
    RequestAnalyticsComputeCostAllocationMethod,
    check_request_analytics_compute_cost_allocation_method,
)
from ..models.request_analytics_compute_cost_basis import (
    RequestAnalyticsComputeCostBasis,
    check_request_analytics_compute_cost_basis,
)
from ..models.request_analytics_compute_cost_currency import (
    RequestAnalyticsComputeCostCurrency,
    check_request_analytics_compute_cost_currency,
)

T = TypeVar("T", bound="RequestAnalyticsComputeCost")


@_attrs_define
class RequestAnalyticsComputeCost:
    """Estimated app compute value over the analytics window, allocated by observed request share. This is not an invoice
    amount; it values raw RAM-hours before the account's included allowance and excludes egress.

    """

    estimated_millicents: int
    allocated_millicents: int
    unallocated_millicents: int
    """Estimated compute value not assigned because the window has no observed route requests."""
    other_route_millicents: int
    """Value allocated to requests outside the top route list."""
    other_route_requests: int
    other_route_request_share_pct: float
    rate_millicents_per_gb_hour: int
    currency: RequestAnalyticsComputeCostCurrency
    allocation_method: RequestAnalyticsComputeCostAllocationMethod
    basis: RequestAnalyticsComputeCostBasis
    request_count: int
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        estimated_millicents = self.estimated_millicents

        allocated_millicents = self.allocated_millicents

        unallocated_millicents = self.unallocated_millicents

        other_route_millicents = self.other_route_millicents

        other_route_requests = self.other_route_requests

        other_route_request_share_pct = self.other_route_request_share_pct

        rate_millicents_per_gb_hour = self.rate_millicents_per_gb_hour

        currency: str = self.currency

        allocation_method: str = self.allocation_method

        basis: str = self.basis

        request_count = self.request_count

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "estimated_millicents": estimated_millicents,
                "allocated_millicents": allocated_millicents,
                "unallocated_millicents": unallocated_millicents,
                "other_route_millicents": other_route_millicents,
                "other_route_requests": other_route_requests,
                "other_route_request_share_pct": other_route_request_share_pct,
                "rate_millicents_per_gb_hour": rate_millicents_per_gb_hour,
                "currency": currency,
                "allocation_method": allocation_method,
                "basis": basis,
                "request_count": request_count,
            }
        )

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        estimated_millicents = d.pop("estimated_millicents")

        allocated_millicents = d.pop("allocated_millicents")

        unallocated_millicents = d.pop("unallocated_millicents")

        other_route_millicents = d.pop("other_route_millicents")

        other_route_requests = d.pop("other_route_requests")

        other_route_request_share_pct = d.pop("other_route_request_share_pct")

        rate_millicents_per_gb_hour = d.pop("rate_millicents_per_gb_hour")

        currency = check_request_analytics_compute_cost_currency(d.pop("currency"))

        allocation_method = check_request_analytics_compute_cost_allocation_method(d.pop("allocation_method"))

        basis = check_request_analytics_compute_cost_basis(d.pop("basis"))

        request_count = d.pop("request_count")

        request_analytics_compute_cost = cls(
            estimated_millicents=estimated_millicents,
            allocated_millicents=allocated_millicents,
            unallocated_millicents=unallocated_millicents,
            other_route_millicents=other_route_millicents,
            other_route_requests=other_route_requests,
            other_route_request_share_pct=other_route_request_share_pct,
            rate_millicents_per_gb_hour=rate_millicents_per_gb_hour,
            currency=currency,
            allocation_method=allocation_method,
            basis=basis,
            request_count=request_count,
        )

        request_analytics_compute_cost.additional_properties = d
        return request_analytics_compute_cost

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
