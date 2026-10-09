from __future__ import annotations

from collections.abc import Mapping
from typing import TYPE_CHECKING, Any, TypeVar

from attrs import define as _attrs_define

from ..models.preview_route_monitor_request_customer_group_by import (
    PreviewRouteMonitorRequestCustomerGroupBy,
    check_preview_route_monitor_request_customer_group_by,
)
from ..types import UNSET, Unset

if TYPE_CHECKING:
    from ..models.route_monitor_route import RouteMonitorRoute


T = TypeVar("T", bound="PreviewRouteMonitorRequest")


@_attrs_define
class PreviewRouteMonitorRequest:
    """Proposed route monitor intent evaluated against recent production observations without being saved."""

    routes: list[RouteMonitorRoute]
    customer_group_by: PreviewRouteMonitorRequestCustomerGroupBy | Unset = UNSET
    """Optional request-time identity dimension for per-cohort evaluation."""

    def to_dict(self) -> dict[str, Any]:
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
        routes = []
        _routes = d.pop("routes")
        for routes_item_data in _routes:
            routes_item = RouteMonitorRoute.from_dict(routes_item_data)

            routes.append(routes_item)

        _customer_group_by = d.pop("customer_group_by", UNSET)
        customer_group_by: PreviewRouteMonitorRequestCustomerGroupBy | Unset
        if isinstance(_customer_group_by, Unset):
            customer_group_by = UNSET
        else:
            customer_group_by = check_preview_route_monitor_request_customer_group_by(_customer_group_by)

        preview_route_monitor_request = cls(
            routes=routes,
            customer_group_by=customer_group_by,
        )

        return preview_route_monitor_request
