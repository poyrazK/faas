from __future__ import annotations

import datetime
from collections.abc import Mapping
from typing import TYPE_CHECKING, Any, TypeVar

from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..models.project_environment_clone_operation_response_status import (
    ProjectEnvironmentCloneOperationResponseStatus,
    check_project_environment_clone_operation_response_status,
)
from ..types import UNSET, Unset

if TYPE_CHECKING:
    from ..models.project_environment_clone_resource_response import ProjectEnvironmentCloneResourceResponse


T = TypeVar("T", bound="ProjectEnvironmentCloneOperationResponse")


@_attrs_define
class ProjectEnvironmentCloneOperationResponse:
    """Durable clone progress within one account and project, excluding private source configuration and worker
    credentials.

    """

    operation_id: str
    project_slug: str
    source_environment: str
    target_environment: str
    source_revision_hash: str
    status: ProjectEnvironmentCloneOperationResponseStatus
    revision: int
    resources: list[ProjectEnvironmentCloneResourceResponse]
    created_at: datetime.datetime
    updated_at: datetime.datetime
    source_release_set_id: str | Unset = UNSET
    target_release_set_id: str | Unset = UNSET
    error_code: str | Unset = UNSET
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        operation_id = self.operation_id

        project_slug = self.project_slug

        source_environment = self.source_environment

        target_environment = self.target_environment

        source_revision_hash = self.source_revision_hash

        status: str = self.status

        revision = self.revision

        resources = []
        for resources_item_data in self.resources:
            resources_item = resources_item_data.to_dict()
            resources.append(resources_item)

        created_at = self.created_at.isoformat()

        updated_at = self.updated_at.isoformat()

        source_release_set_id = self.source_release_set_id

        target_release_set_id = self.target_release_set_id

        error_code = self.error_code

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "operation_id": operation_id,
                "project_slug": project_slug,
                "source_environment": source_environment,
                "target_environment": target_environment,
                "source_revision_hash": source_revision_hash,
                "status": status,
                "revision": revision,
                "resources": resources,
                "created_at": created_at,
                "updated_at": updated_at,
            }
        )
        if source_release_set_id is not UNSET:
            field_dict["source_release_set_id"] = source_release_set_id
        if target_release_set_id is not UNSET:
            field_dict["target_release_set_id"] = target_release_set_id
        if error_code is not UNSET:
            field_dict["error_code"] = error_code

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        from ..models.project_environment_clone_resource_response import ProjectEnvironmentCloneResourceResponse

        d = dict(src_dict)
        operation_id = d.pop("operation_id")

        project_slug = d.pop("project_slug")

        source_environment = d.pop("source_environment")

        target_environment = d.pop("target_environment")

        source_revision_hash = d.pop("source_revision_hash")

        status = check_project_environment_clone_operation_response_status(d.pop("status"))

        revision = d.pop("revision")

        resources = []
        _resources = d.pop("resources")
        for resources_item_data in _resources:
            resources_item = ProjectEnvironmentCloneResourceResponse.from_dict(resources_item_data)

            resources.append(resources_item)

        created_at = datetime.datetime.fromisoformat(d.pop("created_at"))

        updated_at = datetime.datetime.fromisoformat(d.pop("updated_at"))

        source_release_set_id = d.pop("source_release_set_id", UNSET)

        target_release_set_id = d.pop("target_release_set_id", UNSET)

        error_code = d.pop("error_code", UNSET)

        project_environment_clone_operation_response = cls(
            operation_id=operation_id,
            project_slug=project_slug,
            source_environment=source_environment,
            target_environment=target_environment,
            source_revision_hash=source_revision_hash,
            status=status,
            revision=revision,
            resources=resources,
            created_at=created_at,
            updated_at=updated_at,
            source_release_set_id=source_release_set_id,
            target_release_set_id=target_release_set_id,
            error_code=error_code,
        )

        project_environment_clone_operation_response.additional_properties = d
        return project_environment_clone_operation_response

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
