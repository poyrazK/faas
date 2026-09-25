from __future__ import annotations

import datetime
from collections.abc import Mapping
from typing import TYPE_CHECKING, Any, TypeVar

from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..models.project_environment_response_preview_state import (
    ProjectEnvironmentResponsePreviewState,
    check_project_environment_response_preview_state,
)
from ..types import UNSET, Unset

if TYPE_CHECKING:
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
    preview_pr_number: int | Unset = UNSET
    """GitHub pull request number for preview environments."""
    preview_head_sha: str | Unset = UNSET
    """Full lowercase commit SHA recorded for the preview environment."""
    preview_state: ProjectEnvironmentResponsePreviewState | Unset = UNSET
    """Lifecycle state for a PR-scoped project environment."""
    preview_expires_at: datetime.datetime | Unset = UNSET
    """Time when the preview expires or its current cleanup grace period ends."""
    cloned_from: str | Unset = UNSET
    clone: ProjectEnvironmentCloneResponse | Unset = UNSET
    """Non-secret copy counts for an environment clone. Managed database or bucket data appears as shared only
    after explicit opt-in."""
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        id = self.id

        project_id = self.project_id

        slug = self.slug

        protected = self.protected

        created_at = self.created_at.isoformat()

        updated_at = self.updated_at.isoformat()

        preview_pr_number = self.preview_pr_number

        preview_head_sha = self.preview_head_sha

        preview_state: str | Unset = UNSET
        if not isinstance(self.preview_state, Unset):
            preview_state = self.preview_state

        preview_expires_at: str | Unset = UNSET
        if not isinstance(self.preview_expires_at, Unset):
            preview_expires_at = self.preview_expires_at.isoformat()

        cloned_from = self.cloned_from

        clone: dict[str, Any] | Unset = UNSET
        if not isinstance(self.clone, Unset):
            clone = self.clone.to_dict()

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
        if preview_pr_number is not UNSET:
            field_dict["preview_pr_number"] = preview_pr_number
        if preview_head_sha is not UNSET:
            field_dict["preview_head_sha"] = preview_head_sha
        if preview_state is not UNSET:
            field_dict["preview_state"] = preview_state
        if preview_expires_at is not UNSET:
            field_dict["preview_expires_at"] = preview_expires_at
        if cloned_from is not UNSET:
            field_dict["cloned_from"] = cloned_from
        if clone is not UNSET:
            field_dict["clone"] = clone

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        from ..models.project_environment_clone_response import ProjectEnvironmentCloneResponse

        d = dict(src_dict)
        id = d.pop("id")

        project_id = d.pop("project_id")

        slug = d.pop("slug")

        protected = d.pop("protected")

        created_at = datetime.datetime.fromisoformat(d.pop("created_at"))

        updated_at = datetime.datetime.fromisoformat(d.pop("updated_at"))

        preview_pr_number = d.pop("preview_pr_number", UNSET)

        preview_head_sha = d.pop("preview_head_sha", UNSET)

        _preview_state = d.pop("preview_state", UNSET)
        preview_state: ProjectEnvironmentResponsePreviewState | Unset
        if isinstance(_preview_state, Unset):
            preview_state = UNSET
        else:
            preview_state = check_project_environment_response_preview_state(_preview_state)

        _preview_expires_at = d.pop("preview_expires_at", UNSET)
        preview_expires_at: datetime.datetime | Unset
        if isinstance(_preview_expires_at, Unset):
            preview_expires_at = UNSET
        else:
            preview_expires_at = datetime.datetime.fromisoformat(_preview_expires_at)

        cloned_from = d.pop("cloned_from", UNSET)

        _clone = d.pop("clone", UNSET)
        clone: ProjectEnvironmentCloneResponse | Unset
        if isinstance(_clone, Unset):
            clone = UNSET
        else:
            clone = ProjectEnvironmentCloneResponse.from_dict(_clone)

        project_environment_response = cls(
            id=id,
            project_id=project_id,
            slug=slug,
            protected=protected,
            created_at=created_at,
            updated_at=updated_at,
            preview_pr_number=preview_pr_number,
            preview_head_sha=preview_head_sha,
            preview_state=preview_state,
            preview_expires_at=preview_expires_at,
            cloned_from=cloned_from,
            clone=clone,
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
