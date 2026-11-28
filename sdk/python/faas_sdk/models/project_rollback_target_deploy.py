from __future__ import annotations

import datetime
from collections.abc import Mapping
from typing import TYPE_CHECKING, Any, TypeVar
from uuid import UUID

from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..types import UNSET, Unset

if TYPE_CHECKING:
    from ..models.project_rollback_protection import ProjectRollbackProtection


T = TypeVar("T", bound="ProjectRollbackTargetDeploy")


@_attrs_define
class ProjectRollbackTargetDeploy:
    """A production deploy group and any active artifact protection."""

    deploy_group_id: UUID
    created_at: datetime.datetime
    """ Creation time of the newest deployment in the group. """
    service_count: int
    protection: ProjectRollbackProtection | Unset = UNSET
    """ Saved retention window and storage size for a protected deploy group. """
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        deploy_group_id = str(self.deploy_group_id)

        created_at = self.created_at.isoformat()

        service_count = self.service_count

        protection: dict[str, Any] | Unset = UNSET
        if not isinstance(self.protection, Unset):
            protection = self.protection.to_dict()

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "deploy_group_id": deploy_group_id,
                "created_at": created_at,
                "service_count": service_count,
            }
        )
        if protection is not UNSET:
            field_dict["protection"] = protection

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        from ..models.project_rollback_protection import ProjectRollbackProtection

        d = dict(src_dict)
        deploy_group_id = UUID(d.pop("deploy_group_id"))

        created_at = datetime.datetime.fromisoformat(d.pop("created_at"))

        service_count = d.pop("service_count")

        _protection = d.pop("protection", UNSET)
        protection: ProjectRollbackProtection | Unset
        if isinstance(_protection, Unset):
            protection = UNSET
        else:
            protection = ProjectRollbackProtection.from_dict(_protection)

        project_rollback_target_deploy = cls(
            deploy_group_id=deploy_group_id,
            created_at=created_at,
            service_count=service_count,
            protection=protection,
        )

        project_rollback_target_deploy.additional_properties = d
        return project_rollback_target_deploy

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
