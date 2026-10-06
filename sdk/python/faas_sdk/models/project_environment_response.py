from __future__ import annotations

import datetime
from collections.abc import Mapping
from typing import TYPE_CHECKING, Any, TypeVar

from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..types import UNSET, Unset

if TYPE_CHECKING:
    from ..models.project_environment_clone_operation_response import ProjectEnvironmentCloneOperationResponse
    from ..models.project_environment_clone_response import ProjectEnvironmentCloneResponse


T = TypeVar("T", bound="ProjectEnvironmentResponse")


@_attrs_define
class ProjectEnvironmentResponse:
    """Durable named environment target for a project."""

    id: str
    project_id: str
    slug: str
    """Project environment slug; the reserved app scope `default` cannot be used."""
    protected: bool
    created_at: datetime.datetime
    updated_at: datetime.datetime
    cloned_from: str | Unset = UNSET
    clone: ProjectEnvironmentCloneResponse | Unset = UNSET
    """Non-secret copy counts for an environment clone. Managed database or bucket data appears as shared only
    after explicit opt-in."""
    clone_operation: ProjectEnvironmentCloneOperationResponse | Unset = UNSET
    """Durable clone progress within one account and project, excluding private source configuration and worker
    credentials."""
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        id = self.id

        project_id = self.project_id

        slug = self.slug

        protected = self.protected

        created_at = self.created_at.isoformat()

        updated_at = self.updated_at.isoformat()

        cloned_from = self.cloned_from

        clone: dict[str, Any] | Unset = UNSET
        if not isinstance(self.clone, Unset):
            clone = self.clone.to_dict()

        clone_operation: dict[str, Any] | Unset = UNSET
        if not isinstance(self.clone_operation, Unset):
            clone_operation = self.clone_operation.to_dict()

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "id": id,
                "project_id": project_id,
                "slug": slug,
                "protected": protected,
                "created_at": created_at,
                "updated_at": updated_at,
            }
        )
        if cloned_from is not UNSET:
            field_dict["cloned_from"] = cloned_from
        if clone is not UNSET:
            field_dict["clone"] = clone
        if clone_operation is not UNSET:
            field_dict["clone_operation"] = clone_operation

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        from ..models.project_environment_clone_operation_response import ProjectEnvironmentCloneOperationResponse
        from ..models.project_environment_clone_response import ProjectEnvironmentCloneResponse

        d = dict(src_dict)
        id = d.pop("id")

        project_id = d.pop("project_id")

        slug = d.pop("slug")

        protected = d.pop("protected")

        created_at = datetime.datetime.fromisoformat(d.pop("created_at"))

        updated_at = datetime.datetime.fromisoformat(d.pop("updated_at"))

        cloned_from = d.pop("cloned_from", UNSET)

        _clone = d.pop("clone", UNSET)
        clone: ProjectEnvironmentCloneResponse | Unset
        if isinstance(_clone, Unset):
            clone = UNSET
        else:
            clone = ProjectEnvironmentCloneResponse.from_dict(_clone)

        _clone_operation = d.pop("clone_operation", UNSET)
        clone_operation: ProjectEnvironmentCloneOperationResponse | Unset
        if isinstance(_clone_operation, Unset):
            clone_operation = UNSET
        else:
            clone_operation = ProjectEnvironmentCloneOperationResponse.from_dict(_clone_operation)

        project_environment_response = cls(
            id=id,
            project_id=project_id,
            slug=slug,
            protected=protected,
            created_at=created_at,
            updated_at=updated_at,
            cloned_from=cloned_from,
            clone=clone,
            clone_operation=clone_operation,
        )

        project_environment_response.additional_properties = d
        return project_environment_response

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
