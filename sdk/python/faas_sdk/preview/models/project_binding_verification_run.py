from __future__ import annotations

import datetime
from collections.abc import Mapping
from typing import TYPE_CHECKING, Any, TypeVar
from uuid import UUID

from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..models.project_binding_verification_run_status import (
    ProjectBindingVerificationRunStatus,
    check_project_binding_verification_run_status,
)
from ..types import UNSET, Unset

if TYPE_CHECKING:
    from ..models.binding_verification_qualification import BindingVerificationQualification
    from ..models.checked_project_release_manifest import CheckedProjectReleaseManifest
    from ..models.project_binding_verification_run_members import ProjectBindingVerificationRunMembers


T = TypeVar("T", bound="ProjectBindingVerificationRun")


@_attrs_define
class ProjectBindingVerificationRun:
    """Durable project verification graph with pinned members, a shared budget and a fixed deadline."""

    id: UUID
    project: str
    environment: str
    status: ProjectBindingVerificationRunStatus
    created_at: datetime.datetime
    expires_at: datetime.datetime
    review_digest: str
    manifest: CheckedProjectReleaseManifest
    """Immutable candidate graph and expected routing and policy state. This manifest conveys no evidence or
    waiver."""
    object_storage_write: bool
    queue_publish: bool
    members: ProjectBindingVerificationRunMembers
    qualification: BindingVerificationQualification | Unset = UNSET
    """Server-confirmed temporary qualification conditions for the original receipt. No admission or release
    authority; wait within its original expiry and recheck."""
    max_concurrency: int | Unset = 2
    """Project-wide admission budget frozen on this graph receipt and shared by every member run."""
    reason: str | Unset = UNSET
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        id = str(self.id)

        project = self.project

        environment = self.environment

        status: str = self.status

        created_at = self.created_at.isoformat()

        expires_at = self.expires_at.isoformat()

        review_digest = self.review_digest

        manifest = self.manifest.to_dict()

        object_storage_write = self.object_storage_write

        queue_publish = self.queue_publish

        members = self.members.to_dict()

        qualification: dict[str, Any] | Unset = UNSET
        if not isinstance(self.qualification, Unset):
            qualification = self.qualification.to_dict()

        max_concurrency = self.max_concurrency

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
                "review_digest": review_digest,
                "manifest": manifest,
                "object_storage_write": object_storage_write,
                "queue_publish": queue_publish,
                "members": members,
            }
        )
        if qualification is not UNSET:
            field_dict["qualification"] = qualification
        if max_concurrency is not UNSET:
            field_dict["max_concurrency"] = max_concurrency
        if reason is not UNSET:
            field_dict["reason"] = reason

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        from ..models.binding_verification_qualification import BindingVerificationQualification
        from ..models.checked_project_release_manifest import CheckedProjectReleaseManifest
        from ..models.project_binding_verification_run_members import ProjectBindingVerificationRunMembers

        d = dict(src_dict)
        id = UUID(d.pop("id"))

        project = d.pop("project")

        environment = d.pop("environment")

        status = check_project_binding_verification_run_status(d.pop("status"))

        created_at = datetime.datetime.fromisoformat(d.pop("created_at"))

        expires_at = datetime.datetime.fromisoformat(d.pop("expires_at"))

        review_digest = d.pop("review_digest")

        manifest = CheckedProjectReleaseManifest.from_dict(d.pop("manifest"))

        object_storage_write = d.pop("object_storage_write")

        queue_publish = d.pop("queue_publish")

        members = ProjectBindingVerificationRunMembers.from_dict(d.pop("members"))

        _qualification = d.pop("qualification", UNSET)
        qualification: BindingVerificationQualification | Unset
        if isinstance(_qualification, Unset):
            qualification = UNSET
        else:
            qualification = BindingVerificationQualification.from_dict(_qualification)

        max_concurrency = d.pop("max_concurrency", UNSET)

        reason = d.pop("reason", UNSET)

        project_binding_verification_run = cls(
            id=id,
            project=project,
            environment=environment,
            status=status,
            created_at=created_at,
            expires_at=expires_at,
            review_digest=review_digest,
            manifest=manifest,
            object_storage_write=object_storage_write,
            queue_publish=queue_publish,
            members=members,
            qualification=qualification,
            max_concurrency=max_concurrency,
            reason=reason,
        )

        project_binding_verification_run.additional_properties = d
        return project_binding_verification_run

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
