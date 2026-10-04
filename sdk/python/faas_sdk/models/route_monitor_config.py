from __future__ import annotations

import datetime
from collections.abc import Mapping
from typing import TYPE_CHECKING, Any, TypeVar
from uuid import UUID

from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..models.route_monitor_config_customer_group_by import (
    RouteMonitorConfigCustomerGroupBy,
    check_route_monitor_config_customer_group_by,
)
from ..types import UNSET, Unset

if TYPE_CHECKING:
    from ..models.route_monitor_route import RouteMonitorRoute


T = TypeVar("T", bound="RouteMonitorConfig")


@_attrs_define
class RouteMonitorConfig:
    """Advisory production monitor intent, independent of the canary guard."""

    app_id: UUID
    enabled: bool
    revision: int
    routes: list[RouteMonitorRoute]
    customer_group_by: RouteMonitorConfigCustomerGroupBy | Unset = UNSET
    """Saved request-time identity dimension used for per-cohort budget evaluation."""
    updated_at: datetime.datetime | Unset = UNSET
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        app_id = str(self.app_id)

        enabled = self.enabled

        revision = self.revision

        routes = []
        for routes_item_data in self.routes:
            routes_item = routes_item_data.to_dict()
            routes.append(routes_item)

        customer_group_by: str | Unset = UNSET
        if not isinstance(self.customer_group_by, Unset):
            customer_group_by = self.customer_group_by

        updated_at: str | Unset = UNSET
        if not isinstance(self.updated_at, Unset):
            updated_at = self.updated_at.isoformat()

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "app_id": app_id,
                "enabled": enabled,
                "revision": revision,
                "routes": routes,
            }
        )
        if customer_group_by is not UNSET:
            field_dict["customer_group_by"] = customer_group_by
        if updated_at is not UNSET:
            field_dict["updated_at"] = updated_at

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        from ..models.route_monitor_route import RouteMonitorRoute

        d = dict(src_dict)
        app_id = UUID(d.pop("app_id"))

        enabled = d.pop("enabled")

        revision = d.pop("revision")

        routes = []
        _routes = d.pop("routes")
        for routes_item_data in _routes:
            routes_item = RouteMonitorRoute.from_dict(routes_item_data)

            routes.append(routes_item)

        _customer_group_by = d.pop("customer_group_by", UNSET)
        customer_group_by: RouteMonitorConfigCustomerGroupBy | Unset
        if isinstance(_customer_group_by, Unset):
            customer_group_by = UNSET
        else:
            customer_group_by = check_route_monitor_config_customer_group_by(_customer_group_by)

        _updated_at = d.pop("updated_at", UNSET)
        updated_at: datetime.datetime | Unset
        if isinstance(_updated_at, Unset):
            updated_at = UNSET
        else:
            updated_at = datetime.datetime.fromisoformat(_updated_at)

        route_monitor_config = cls(
            app_id=app_id,
            enabled=enabled,
            revision=revision,
            routes=routes,
            customer_group_by=customer_group_by,
            updated_at=updated_at,
        )

        route_monitor_config.additional_properties = d
        return route_monitor_config

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
