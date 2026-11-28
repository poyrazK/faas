from __future__ import annotations

import datetime
from collections.abc import Mapping
from typing import TYPE_CHECKING, Any, TypeVar

from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..models.route_customer_usage_method import RouteCustomerUsageMethod, check_route_customer_usage_method

if TYPE_CHECKING:
    from ..models.route_customer_observation import RouteCustomerObservation


T = TypeVar("T", bound="RouteCustomerUsage")


@_attrs_define
class RouteCustomerUsage:
    """Observed customer exposure for one exact route label and HTTP method on the selected deployment."""

    route: str
    """Exact recorded route label; may include its HTTP method prefix."""
    method: RouteCustomerUsageMethod
    """HTTP method for this route observation."""
    requests: int
    """All weighted retained requests for this route and deployment."""
    identified_requests: int
    """Requests with at least one resolvable account-owned consumer or tenant identity."""
    anonymous_requests: int
    """Requests with neither consumer nor tenant identity recorded."""
    unresolved_identity_requests: int
    """Requests with recorded identities that cannot be resolved within the app and account boundary."""
    consumer_count: int
    """Distinct observed app-owned consumers before detail caps; overlaps tenant count."""
    platform_tenant_count: int
    """Distinct observed account-owned request-time tenants before detail caps; overlaps consumer count."""
    last_observed_at: datetime.datetime
    """Latest recorded timestamp for this route; may be a minute bucket."""
    customers: list[RouteCustomerObservation]
    """Top recorded identity pairs by request count, with deterministic ID tie ordering."""
    customers_truncated: bool
    """More identity pairs matched than the customer detail limit."""
    other_customer_requests: int
    """Identified requests belonging to identity pairs omitted by the detail cap."""
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        route = self.route

        method: str = self.method

        requests = self.requests

        identified_requests = self.identified_requests

        anonymous_requests = self.anonymous_requests

        unresolved_identity_requests = self.unresolved_identity_requests

        consumer_count = self.consumer_count

        platform_tenant_count = self.platform_tenant_count

        last_observed_at = self.last_observed_at.isoformat()

        customers = []
        for customers_item_data in self.customers:
            customers_item = customers_item_data.to_dict()
            customers.append(customers_item)

        customers_truncated = self.customers_truncated

        other_customer_requests = self.other_customer_requests

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "route": route,
                "method": method,
                "requests": requests,
                "identified_requests": identified_requests,
                "anonymous_requests": anonymous_requests,
                "unresolved_identity_requests": unresolved_identity_requests,
                "consumer_count": consumer_count,
                "platform_tenant_count": platform_tenant_count,
                "last_observed_at": last_observed_at,
                "customers": customers,
                "customers_truncated": customers_truncated,
                "other_customer_requests": other_customer_requests,
            }
        )

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        from ..models.route_customer_observation import RouteCustomerObservation

        d = dict(src_dict)
        route = d.pop("route")

        method = check_route_customer_usage_method(d.pop("method"))

        requests = d.pop("requests")

        identified_requests = d.pop("identified_requests")

        anonymous_requests = d.pop("anonymous_requests")

        unresolved_identity_requests = d.pop("unresolved_identity_requests")

        consumer_count = d.pop("consumer_count")

        platform_tenant_count = d.pop("platform_tenant_count")

        last_observed_at = datetime.datetime.fromisoformat(d.pop("last_observed_at"))

        customers = []
        _customers = d.pop("customers")
        for customers_item_data in _customers:
            customers_item = RouteCustomerObservation.from_dict(customers_item_data)

            customers.append(customers_item)

        customers_truncated = d.pop("customers_truncated")

        other_customer_requests = d.pop("other_customer_requests")

        route_customer_usage = cls(
            route=route,
            method=method,
            requests=requests,
            identified_requests=identified_requests,
            anonymous_requests=anonymous_requests,
            unresolved_identity_requests=unresolved_identity_requests,
            consumer_count=consumer_count,
            platform_tenant_count=platform_tenant_count,
            last_observed_at=last_observed_at,
            customers=customers,
            customers_truncated=customers_truncated,
            other_customer_requests=other_customer_requests,
        )

        route_customer_usage.additional_properties = d
        return route_customer_usage

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
