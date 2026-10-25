from __future__ import annotations

import datetime
from collections.abc import Mapping
from typing import TYPE_CHECKING, Any, TypeVar
from uuid import UUID

from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..models.route_customer_usage_response_coverage import (
    RouteCustomerUsageResponseCoverage,
    check_route_customer_usage_response_coverage,
)

if TYPE_CHECKING:
    from ..models.route_customer_usage import RouteCustomerUsage


T = TypeVar("T", bound="RouteCustomerUsageResponse")


@_attrs_define
class RouteCustomerUsageResponse:
    """Bounded observed customer usage for one app, deployment and half-open retained telemetry window."""

    slug: str
    """App whose deployment and telemetry were read."""
    deployment_id: UUID
    """Selected immutable deployment owned by this app."""
    from_: datetime.datetime
    """Effective inclusive lower bound after retention clamping."""
    until: datetime.datetime
    """Exclusive upper bound of the observation window."""
    as_of: datetime.datetime
    """UTC time at which the read window was assembled."""
    window_clamped: bool
    """Requested start was older than current retained telemetry."""
    coverage: RouteCustomerUsageResponseCoverage
    """Complete capture cannot be established from retained telemetry."""
    routes: list[RouteCustomerUsage]
    """Top route/method observations ordered by weighted requests, route, then method."""
    routes_limit: int
    """Maximum route observations returned."""
    routes_truncated: bool
    """More route observations matched than the route limit; missing routes remain unknown."""
    customers_limit: int
    """Maximum identity groups returned per route; aggregate counts precede this cap."""
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        slug = self.slug

        deployment_id = str(self.deployment_id)

        from_ = self.from_.isoformat()

        until = self.until.isoformat()

        as_of = self.as_of.isoformat()

        window_clamped = self.window_clamped

        coverage: str = self.coverage

        routes = []
        for routes_item_data in self.routes:
            routes_item = routes_item_data.to_dict()
            routes.append(routes_item)

        routes_limit = self.routes_limit

        routes_truncated = self.routes_truncated

        customers_limit = self.customers_limit

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "slug": slug,
                "deployment_id": deployment_id,
                "from": from_,
                "until": until,
                "as_of": as_of,
                "window_clamped": window_clamped,
                "coverage": coverage,
                "routes": routes,
                "routes_limit": routes_limit,
                "routes_truncated": routes_truncated,
                "customers_limit": customers_limit,
            }
        )

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        from ..models.route_customer_usage import RouteCustomerUsage

        d = dict(src_dict)
        slug = d.pop("slug")

        deployment_id = UUID(d.pop("deployment_id"))

        from_ = datetime.datetime.fromisoformat(d.pop("from"))

        until = datetime.datetime.fromisoformat(d.pop("until"))

        as_of = datetime.datetime.fromisoformat(d.pop("as_of"))

        window_clamped = d.pop("window_clamped")

        coverage = check_route_customer_usage_response_coverage(d.pop("coverage"))

        routes = []
        _routes = d.pop("routes")
        for routes_item_data in _routes:
            routes_item = RouteCustomerUsage.from_dict(routes_item_data)

            routes.append(routes_item)

        routes_limit = d.pop("routes_limit")

        routes_truncated = d.pop("routes_truncated")

        customers_limit = d.pop("customers_limit")

        route_customer_usage_response = cls(
            slug=slug,
            deployment_id=deployment_id,
            from_=from_,
            until=until,
            as_of=as_of,
            window_clamped=window_clamped,
            coverage=coverage,
            routes=routes,
            routes_limit=routes_limit,
            routes_truncated=routes_truncated,
            customers_limit=customers_limit,
        )

        route_customer_usage_response.additional_properties = d
        return route_customer_usage_response

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
