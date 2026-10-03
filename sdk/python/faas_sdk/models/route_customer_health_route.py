from __future__ import annotations

from collections.abc import Mapping
from typing import TYPE_CHECKING, Any, TypeVar

from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..models.route_customer_health_route_method import (
    RouteCustomerHealthRouteMethod,
    check_route_customer_health_route_method,
)

if TYPE_CHECKING:
    from ..models.route_customer_health_attribution import RouteCustomerHealthAttribution
    from ..models.route_customer_health_cohort import RouteCustomerHealthCohort


T = TypeVar("T", bound="RouteCustomerHealthRoute")


@_attrs_define
class RouteCustomerHealthRoute:
    """Exact selected route with bounded identity cohorts from the union of candidate and stable observations. Ranking uses
    candidate 5xx counts descending, then combined request volume descending, then UUID ascending. Totals precede caps.

    """

    method: RouteCustomerHealthRouteMethod
    path: str
    observed_customers: int
    customers_truncated: bool
    candidate: RouteCustomerHealthAttribution
    """Weighted request attribution for one selected identity dimension across both shared windows. Missing
    identities and unresolved scoped identities remain separate. Other requests are identified traffic outside the
    cohort output cap."""
    stable: RouteCustomerHealthAttribution
    """Weighted request attribution for one selected identity dimension across both shared windows. Missing
    identities and unresolved scoped identities remain separate. Other requests are identified traffic outside the
    cohort output cap."""
    customers: list[RouteCustomerHealthCohort]
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        method: str = self.method

        path = self.path

        observed_customers = self.observed_customers

        customers_truncated = self.customers_truncated

        candidate = self.candidate.to_dict()

        stable = self.stable.to_dict()

        customers = []
        for customers_item_data in self.customers:
            customers_item = customers_item_data.to_dict()
            customers.append(customers_item)

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "method": method,
                "path": path,
                "observed_customers": observed_customers,
                "customers_truncated": customers_truncated,
                "candidate": candidate,
                "stable": stable,
                "customers": customers,
            }
        )

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        from ..models.route_customer_health_attribution import RouteCustomerHealthAttribution
        from ..models.route_customer_health_cohort import RouteCustomerHealthCohort

        d = dict(src_dict)
        method = check_route_customer_health_route_method(d.pop("method"))

        path = d.pop("path")

        observed_customers = d.pop("observed_customers")

        customers_truncated = d.pop("customers_truncated")

        candidate = RouteCustomerHealthAttribution.from_dict(d.pop("candidate"))

        stable = RouteCustomerHealthAttribution.from_dict(d.pop("stable"))

        customers = []
        _customers = d.pop("customers")
        for customers_item_data in _customers:
            customers_item = RouteCustomerHealthCohort.from_dict(customers_item_data)

            customers.append(customers_item)

        route_customer_health_route = cls(
            method=method,
            path=path,
            observed_customers=observed_customers,
            customers_truncated=customers_truncated,
            candidate=candidate,
            stable=stable,
            customers=customers,
        )

        route_customer_health_route.additional_properties = d
        return route_customer_health_route

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
