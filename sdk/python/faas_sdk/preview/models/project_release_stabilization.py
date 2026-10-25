from __future__ import annotations

import datetime
from collections.abc import Mapping
from typing import Any, TypeVar

from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..models.project_release_stabilization_health import (
    ProjectReleaseStabilizationHealth,
    check_project_release_stabilization_health,
)
from ..models.project_release_stabilization_status import (
    ProjectReleaseStabilizationStatus,
    check_project_release_stabilization_status,
)
from ..types import UNSET, Unset

T = TypeVar("T", bound="ProjectReleaseStabilization")


@_attrs_define
class ProjectReleaseStabilization:
    """Opt-in bounded health monitoring after progressive rollout success."""

    enabled: bool
    status: ProjectReleaseStabilizationStatus
    health: ProjectReleaseStabilizationHealth | Unset = UNSET
    started_at: datetime.datetime | Unset = UNSET
    deadline: datetime.datetime | Unset = UNSET
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        enabled = self.enabled

        status: str = self.status

        health: str | Unset = UNSET
        if not isinstance(self.health, Unset):
            health = self.health

        started_at: str | Unset = UNSET
        if not isinstance(self.started_at, Unset):
            started_at = self.started_at.isoformat()

        deadline: str | Unset = UNSET
        if not isinstance(self.deadline, Unset):
            deadline = self.deadline.isoformat()

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "enabled": enabled,
                "status": status,
            }
        )
        if health is not UNSET:
            field_dict["health"] = health
        if started_at is not UNSET:
            field_dict["started_at"] = started_at
        if deadline is not UNSET:
            field_dict["deadline"] = deadline

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        enabled = d.pop("enabled")

        status = check_project_release_stabilization_status(d.pop("status"))

        _health = d.pop("health", UNSET)
        health: ProjectReleaseStabilizationHealth | Unset
        if isinstance(_health, Unset):
            health = UNSET
        else:
            health = check_project_release_stabilization_health(_health)

        _started_at = d.pop("started_at", UNSET)
        started_at: datetime.datetime | Unset
        if isinstance(_started_at, Unset):
            started_at = UNSET
        else:
            started_at = datetime.datetime.fromisoformat(_started_at)

        _deadline = d.pop("deadline", UNSET)
        deadline: datetime.datetime | Unset
        if isinstance(_deadline, Unset):
            deadline = UNSET
        else:
            deadline = datetime.datetime.fromisoformat(_deadline)

        project_release_stabilization = cls(
            enabled=enabled,
            status=status,
            health=health,
            started_at=started_at,
            deadline=deadline,
        )

        project_release_stabilization.additional_properties = d
        return project_release_stabilization

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
