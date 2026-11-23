from __future__ import annotations

from collections.abc import Mapping
from typing import TYPE_CHECKING, Any, TypeVar
from uuid import UUID

from attrs import define as _attrs_define
from attrs import field as _attrs_field

if TYPE_CHECKING:
    from ..models.project_rollback_target_deploy import ProjectRollbackTargetDeploy


T = TypeVar("T", bound="ProjectRollbackTargets")


@_attrs_define
class ProjectRollbackTargets:
    """Recorded project deploy groups available for rollback preview."""

    project: str
    project_id: UUID
    deploys: list[ProjectRollbackTargetDeploy]
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        project = self.project

        project_id = str(self.project_id)

        deploys = []
        for deploys_item_data in self.deploys:
            deploys_item = deploys_item_data.to_dict()
            deploys.append(deploys_item)

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "project": project,
                "project_id": project_id,
                "deploys": deploys,
            }
        )

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        from ..models.project_rollback_target_deploy import ProjectRollbackTargetDeploy

        d = dict(src_dict)
        project = d.pop("project")

        project_id = UUID(d.pop("project_id"))

        deploys = []
        _deploys = d.pop("deploys")
        for deploys_item_data in _deploys:
            deploys_item = ProjectRollbackTargetDeploy.from_dict(deploys_item_data)

            deploys.append(deploys_item)

        project_rollback_targets = cls(
            project=project,
            project_id=project_id,
            deploys=deploys,
        )

        project_rollback_targets.additional_properties = d
        return project_rollback_targets

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
