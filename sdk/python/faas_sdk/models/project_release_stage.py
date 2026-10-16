from __future__ import annotations

import datetime
from collections.abc import Mapping
from typing import TYPE_CHECKING, Any, TypeVar
from uuid import UUID

from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..models.project_release_stage_status import ProjectReleaseStageStatus, check_project_release_stage_status
from ..types import UNSET, Unset

if TYPE_CHECKING:
    from ..models.project_release_stage_member import ProjectReleaseStageMember


T = TypeVar("T", bound="ProjectReleaseStage")


@_attrs_define
class ProjectReleaseStage:
    """Bounded lease for temporarily admitting candidate project services; observed readiness is not binding evidence."""

    id: UUID
    project: str
    environment: str
    status: ProjectReleaseStageStatus
    created_at: datetime.datetime
    expires_at: datetime.datetime
    members: list[ProjectReleaseStageMember]
    reason: str | Unset = UNSET
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        id = str(self.id)

        project = self.project

        environment = self.environment

        status: str = self.status

        created_at = self.created_at.isoformat()

        expires_at = self.expires_at.isoformat()

        members = []
        for members_item_data in self.members:
            members_item = members_item_data.to_dict()
            members.append(members_item)

        reason = self.reason

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "id": id,
                "project": project,
                "environment": environment,
                "status": status,
                "created_at": created_at,
                "expires_at": expires_at,
                "members": members,
            }
        )
        if reason is not UNSET:
            field_dict["reason"] = reason

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        from ..models.project_release_stage_member import ProjectReleaseStageMember

        d = dict(src_dict)
        id = UUID(d.pop("id"))

        project = d.pop("project")

        environment = d.pop("environment")

        status = check_project_release_stage_status(d.pop("status"))

        created_at = datetime.datetime.fromisoformat(d.pop("created_at"))

        expires_at = datetime.datetime.fromisoformat(d.pop("expires_at"))

        members = []
        _members = d.pop("members")
        for members_item_data in _members:
            members_item = ProjectReleaseStageMember.from_dict(members_item_data)

            members.append(members_item)

        reason = d.pop("reason", UNSET)

        project_release_stage = cls(
            id=id,
            project=project,
            environment=environment,
            status=status,
            created_at=created_at,
            expires_at=expires_at,
            members=members,
            reason=reason,
        )

        project_release_stage.additional_properties = d
        return project_release_stage

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
