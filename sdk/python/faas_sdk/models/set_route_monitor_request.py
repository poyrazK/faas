from __future__ import annotations

from collections.abc import Mapping
from typing import TYPE_CHECKING, Any, TypeVar

from attrs import define as _attrs_define

from ..models.set_route_monitor_request_customer_group_by import (
    SetRouteMonitorRequestCustomerGroupBy,
    check_set_route_monitor_request_customer_group_by,
)
from ..types import UNSET, Unset

if TYPE_CHECKING:
    from ..models.route_monitor_route import RouteMonitorRoute


T = TypeVar("T", bound="SetRouteMonitorRequest")


@_attrs_define
class SetRouteMonitorRequest:
    """Replacement production monitor intent, with a mandatory revision check."""

    enabled: bool
    expected_revision: int
    routes: list[RouteMonitorRoute]
    customer_group_by: SetRouteMonitorRequestCustomerGroupBy | Unset = UNSET
    """Optional request-time identity dimension. Omission disables per-cohort evaluation."""

    def to_dict(self) -> dict[str, Any]:
        enabled = self.enabled

        expected_revision = self.expected_revision

        routes = []
        for routes_item_data in self.routes:
            routes_item = routes_item_data.to_dict()
            routes.append(routes_item)

        customer_group_by: str | Unset = UNSET
        if not isinstance(self.customer_group_by, Unset):
            customer_group_by = self.customer_group_by

        field_dict: dict[str, Any] = {}

        field_dict.update(
            {
                "enabled": enabled,
                "expected_revision": expected_revision,
                "routes": routes,
            }
        )
        if customer_group_by is not UNSET:
            field_dict["customer_group_by"] = customer_group_by

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        from ..models.route_monitor_route import RouteMonitorRoute

        d = dict(src_dict)
        enabled = d.pop("enabled")

        expected_revision = d.pop("expected_revision")

        routes = []
        _routes = d.pop("routes")
        for routes_item_data in _routes:
            routes_item = RouteMonitorRoute.from_dict(routes_item_data)

            routes.append(routes_item)

        _customer_group_by = d.pop("customer_group_by", UNSET)
        customer_group_by: SetRouteMonitorRequestCustomerGroupBy | Unset
        if isinstance(_customer_group_by, Unset):
            customer_group_by = UNSET
        else:
            customer_group_by = check_set_route_monitor_request_customer_group_by(_customer_group_by)

        set_route_monitor_request = cls(
            enabled=enabled,
            expected_revision=expected_revision,
            routes=routes,
            customer_group_by=customer_group_by,
        )

        return set_route_monitor_request
