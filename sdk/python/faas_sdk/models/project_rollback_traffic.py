from __future__ import annotations

import datetime
from collections.abc import Mapping
from typing import Any, TypeVar

from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..models.project_rollback_traffic_status import ProjectRollbackTrafficStatus, check_project_rollback_traffic_status
from ..types import UNSET, Unset

T = TypeVar("T", bound="ProjectRollbackTraffic")


@_attrs_define
class ProjectRollbackTraffic:
    """Observed HTTP recovery for the exact restored deployment."""

    status: ProjectRollbackTrafficStatus
    route_generation: int | Unset = UNSET
    routing_committed_at: datetime.datetime | Unset = UNSET
    gateways_applied_at: datetime.datetime | Unset = UNSET
    first_success_at: datetime.datetime | Unset = UNSET
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        status: str = self.status

        route_generation = self.route_generation

        routing_committed_at: str | Unset = UNSET
        if not isinstance(self.routing_committed_at, Unset):
            routing_committed_at = self.routing_committed_at.isoformat()

        gateways_applied_at: str | Unset = UNSET
        if not isinstance(self.gateways_applied_at, Unset):
            gateways_applied_at = self.gateways_applied_at.isoformat()

        first_success_at: str | Unset = UNSET
        if not isinstance(self.first_success_at, Unset):
            first_success_at = self.first_success_at.isoformat()

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "status": status,
            }
        )
        if route_generation is not UNSET:
            field_dict["route_generation"] = route_generation
        if routing_committed_at is not UNSET:
            field_dict["routing_committed_at"] = routing_committed_at
        if gateways_applied_at is not UNSET:
            field_dict["gateways_applied_at"] = gateways_applied_at
        if first_success_at is not UNSET:
            field_dict["first_success_at"] = first_success_at

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        status = check_project_rollback_traffic_status(d.pop("status"))

        route_generation = d.pop("route_generation", UNSET)

        _routing_committed_at = d.pop("routing_committed_at", UNSET)
        routing_committed_at: datetime.datetime | Unset
        if isinstance(_routing_committed_at, Unset):
            routing_committed_at = UNSET
        else:
            routing_committed_at = datetime.datetime.fromisoformat(_routing_committed_at)

        _gateways_applied_at = d.pop("gateways_applied_at", UNSET)
        gateways_applied_at: datetime.datetime | Unset
        if isinstance(_gateways_applied_at, Unset):
            gateways_applied_at = UNSET
        else:
            gateways_applied_at = datetime.datetime.fromisoformat(_gateways_applied_at)

        _first_success_at = d.pop("first_success_at", UNSET)
        first_success_at: datetime.datetime | Unset
        if isinstance(_first_success_at, Unset):
            first_success_at = UNSET
        else:
            first_success_at = datetime.datetime.fromisoformat(_first_success_at)

        project_rollback_traffic = cls(
            status=status,
            route_generation=route_generation,
            routing_committed_at=routing_committed_at,
            gateways_applied_at=gateways_applied_at,
            first_success_at=first_success_at,
        )

        project_rollback_traffic.additional_properties = d
        return project_rollback_traffic

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
