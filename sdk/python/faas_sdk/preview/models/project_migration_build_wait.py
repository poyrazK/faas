from __future__ import annotations

import datetime
from collections.abc import Mapping
from typing import Any, TypeVar

from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..models.project_migration_build_wait_status import (
    ProjectMigrationBuildWaitStatus,
    check_project_migration_build_wait_status,
)
from ..types import UNSET, Unset

T = TypeVar("T", bound="ProjectMigrationBuildWait")


@_attrs_define
class ProjectMigrationBuildWait:
    """Durable readiness progress before a migration build consumes a builder slot. Public URLs still route normally; this
    is a point-in-time readiness check.

    """

    status: ProjectMigrationBuildWaitStatus
    """Whether selected API deployments satisfied build admission."""
    deadline_at: datetime.datetime
    """Fixed deadline for this attempt's API readiness wait."""
    code: str | Unset = UNSET
    """Stable build dependency blocker code, when present."""
    detail: str | Unset = UNSET
    """Customer-safe explanation of a build dependency wait or failure."""
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        status: str = self.status

        deadline_at = self.deadline_at.isoformat()

        code = self.code

        detail = self.detail

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "status": status,
                "deadline_at": deadline_at,
            }
        )
        if code is not UNSET:
            field_dict["code"] = code
        if detail is not UNSET:
            field_dict["detail"] = detail

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        status = check_project_migration_build_wait_status(d.pop("status"))

        deadline_at = datetime.datetime.fromisoformat(d.pop("deadline_at"))

        code = d.pop("code", UNSET)

        detail = d.pop("detail", UNSET)

        project_migration_build_wait = cls(
            status=status,
            deadline_at=deadline_at,
            code=code,
            detail=detail,
        )

        project_migration_build_wait.additional_properties = d
        return project_migration_build_wait

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
