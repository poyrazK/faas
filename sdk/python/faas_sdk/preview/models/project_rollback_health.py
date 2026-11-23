from __future__ import annotations

import datetime
from collections.abc import Mapping
from typing import Any, TypeVar

from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..types import UNSET, Unset

T = TypeVar("T", bound="ProjectRollbackHealth")


@_attrs_define
class ProjectRollbackHealth:
    """Durable outcome of the configured HTTP health probes for the exact restored route."""

    status: str
    """ One of pending, awaiting_gateways, awaiting_probe, stabilizing, passed, or failed. """
    deadline: datetime.datetime | Unset = UNSET
    checked_at: datetime.datetime | Unset = UNSET
    healthy_since: datetime.datetime | Unset = UNSET
    """ First successful probe in the continuous stabilization window. """
    route_generation: int | Unset = UNSET
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        status = self.status

        deadline: str | Unset = UNSET
        if not isinstance(self.deadline, Unset):
            deadline = self.deadline.isoformat()

        checked_at: str | Unset = UNSET
        if not isinstance(self.checked_at, Unset):
            checked_at = self.checked_at.isoformat()

        healthy_since: str | Unset = UNSET
        if not isinstance(self.healthy_since, Unset):
            healthy_since = self.healthy_since.isoformat()

        route_generation = self.route_generation

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "status": status,
            }
        )
        if deadline is not UNSET:
            field_dict["deadline"] = deadline
        if checked_at is not UNSET:
            field_dict["checked_at"] = checked_at
        if healthy_since is not UNSET:
            field_dict["healthy_since"] = healthy_since
        if route_generation is not UNSET:
            field_dict["route_generation"] = route_generation

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        status = d.pop("status")

        _deadline = d.pop("deadline", UNSET)
        deadline: datetime.datetime | Unset
        if isinstance(_deadline, Unset):
            deadline = UNSET
        else:
            deadline = datetime.datetime.fromisoformat(_deadline)

        _checked_at = d.pop("checked_at", UNSET)
        checked_at: datetime.datetime | Unset
        if isinstance(_checked_at, Unset):
            checked_at = UNSET
        else:
            checked_at = datetime.datetime.fromisoformat(_checked_at)

        _healthy_since = d.pop("healthy_since", UNSET)
        healthy_since: datetime.datetime | Unset
        if isinstance(_healthy_since, Unset):
            healthy_since = UNSET
        else:
            healthy_since = datetime.datetime.fromisoformat(_healthy_since)

        route_generation = d.pop("route_generation", UNSET)

        project_rollback_health = cls(
            status=status,
            deadline=deadline,
            checked_at=checked_at,
            healthy_since=healthy_since,
            route_generation=route_generation,
        )

        project_rollback_health.additional_properties = d
        return project_rollback_health

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
