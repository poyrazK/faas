from __future__ import annotations

import datetime
from collections.abc import Mapping
from typing import Any, TypeVar

from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..models.app_private_network_node_status_fabric_status import (
    AppPrivateNetworkNodeStatusFabricStatus,
    check_app_private_network_node_status_fabric_status,
)
from ..models.app_private_network_node_status_route_status import (
    AppPrivateNetworkNodeStatusRouteStatus,
    check_app_private_network_node_status_route_status,
)
from ..types import UNSET, Unset

T = TypeVar("T", bound="AppPrivateNetworkNodeStatus")


@_attrs_define
class AppPrivateNetworkNodeStatus:
    """Last durable fabric and route convergence result for one compute node."""

    node_id: str
    """Compute-node identity that reported the observation."""
    fabric_status: AppPrivateNetworkNodeStatusFabricStatus | Unset = UNSET
    fabric_detail: str | Unset = UNSET
    route_status: AppPrivateNetworkNodeStatusRouteStatus | Unset = UNSET
    route_detail: str | Unset = UNSET
    observed_at: datetime.datetime | Unset = UNSET
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        node_id = self.node_id

        fabric_status: str | Unset = UNSET
        if not isinstance(self.fabric_status, Unset):
            fabric_status = self.fabric_status

        fabric_detail = self.fabric_detail

        route_status: str | Unset = UNSET
        if not isinstance(self.route_status, Unset):
            route_status = self.route_status

        route_detail = self.route_detail

        observed_at: str | Unset = UNSET
        if not isinstance(self.observed_at, Unset):
            observed_at = self.observed_at.isoformat()

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "node_id": node_id,
            }
        )
        if fabric_status is not UNSET:
            field_dict["fabric_status"] = fabric_status
        if fabric_detail is not UNSET:
            field_dict["fabric_detail"] = fabric_detail
        if route_status is not UNSET:
            field_dict["route_status"] = route_status
        if route_detail is not UNSET:
            field_dict["route_detail"] = route_detail
        if observed_at is not UNSET:
            field_dict["observed_at"] = observed_at

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        node_id = d.pop("node_id")

        _fabric_status = d.pop("fabric_status", UNSET)
        fabric_status: AppPrivateNetworkNodeStatusFabricStatus | Unset
        if isinstance(_fabric_status, Unset):
            fabric_status = UNSET
        else:
            fabric_status = check_app_private_network_node_status_fabric_status(_fabric_status)

        fabric_detail = d.pop("fabric_detail", UNSET)

        _route_status = d.pop("route_status", UNSET)
        route_status: AppPrivateNetworkNodeStatusRouteStatus | Unset
        if isinstance(_route_status, Unset):
            route_status = UNSET
        else:
            route_status = check_app_private_network_node_status_route_status(_route_status)

        route_detail = d.pop("route_detail", UNSET)

        _observed_at = d.pop("observed_at", UNSET)
        observed_at: datetime.datetime | Unset
        if isinstance(_observed_at, Unset):
            observed_at = UNSET
        else:
            observed_at = datetime.datetime.fromisoformat(_observed_at)

        app_private_network_node_status = cls(
            node_id=node_id,
            fabric_status=fabric_status,
            fabric_detail=fabric_detail,
            route_status=route_status,
            route_detail=route_detail,
            observed_at=observed_at,
        )

        app_private_network_node_status.additional_properties = d
        return app_private_network_node_status

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
