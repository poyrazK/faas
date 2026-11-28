from __future__ import annotations

import datetime
from collections.abc import Mapping
from typing import Any, TypeVar

from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..models.project_release_control_action import ProjectReleaseControlAction, check_project_release_control_action
from ..models.project_release_control_status import ProjectReleaseControlStatus, check_project_release_control_status

T = TypeVar("T", bound="ProjectReleaseControl")


@_attrs_define
class ProjectReleaseControl:
    """Most recent durable operator decision and its restoration outcome."""

    action: ProjectReleaseControlAction
    request_id: str
    expected_generation: int
    requested_at: datetime.datetime
    status: ProjectReleaseControlStatus
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        action: str = self.action

        request_id = self.request_id

        expected_generation = self.expected_generation

        requested_at = self.requested_at.isoformat()

        status: str = self.status

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "action": action,
                "request_id": request_id,
                "expected_generation": expected_generation,
                "requested_at": requested_at,
                "status": status,
            }
        )

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        action = check_project_release_control_action(d.pop("action"))

        request_id = d.pop("request_id")

        expected_generation = d.pop("expected_generation")

        requested_at = datetime.datetime.fromisoformat(d.pop("requested_at"))

        status = check_project_release_control_status(d.pop("status"))

        project_release_control = cls(
            action=action,
            request_id=request_id,
            expected_generation=expected_generation,
            requested_at=requested_at,
            status=status,
        )

        project_release_control.additional_properties = d
        return project_release_control

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
