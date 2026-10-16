from __future__ import annotations

import datetime
from collections.abc import Mapping
from typing import TYPE_CHECKING, Any, TypeVar

from attrs import define as _attrs_define
from attrs import field as _attrs_field

if TYPE_CHECKING:
    from ..models.binding_check_finding import BindingCheckFinding
    from ..models.checked_project_release_manifest import CheckedProjectReleaseManifest
    from ..models.project_binding_verification_review_members import ProjectBindingVerificationReviewMembers
    from ..models.project_binding_verification_review_review_digests import (
        ProjectBindingVerificationReviewReviewDigests,
    )


T = TypeVar("T", bound="ProjectBindingVerificationReview")


@_attrs_define
class ProjectBindingVerificationReview:
    """Read-only project verification plan, blockers, required consent and opaque review digests."""

    project: str
    environment: str
    reviewed_at: datetime.datetime
    ready: bool
    review_digest: str
    review_digests: ProjectBindingVerificationReviewReviewDigests
    manifest: CheckedProjectReleaseManifest
    """Immutable candidate graph and expected routing and policy state. This manifest conveys no evidence or
    waiver."""
    object_storage_write: bool
    queue_publish: bool
    object_storage_write_required: bool
    queue_publish_required: bool
    members: ProjectBindingVerificationReviewMembers
    blockers: list[BindingCheckFinding]
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        project = self.project

        environment = self.environment

        reviewed_at = self.reviewed_at.isoformat()

        ready = self.ready

        review_digest = self.review_digest

        review_digests = self.review_digests.to_dict()

        manifest = self.manifest.to_dict()

        object_storage_write = self.object_storage_write

        queue_publish = self.queue_publish

        object_storage_write_required = self.object_storage_write_required

        queue_publish_required = self.queue_publish_required

        members = self.members.to_dict()

        blockers = []
        for blockers_item_data in self.blockers:
            blockers_item = blockers_item_data.to_dict()
            blockers.append(blockers_item)

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "project": project,
                "environment": environment,
                "reviewed_at": reviewed_at,
                "ready": ready,
                "review_digest": review_digest,
                "review_digests": review_digests,
                "manifest": manifest,
                "object_storage_write": object_storage_write,
                "queue_publish": queue_publish,
                "object_storage_write_required": object_storage_write_required,
                "queue_publish_required": queue_publish_required,
                "members": members,
                "blockers": blockers,
            }
        )

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        from ..models.binding_check_finding import BindingCheckFinding
        from ..models.checked_project_release_manifest import CheckedProjectReleaseManifest
        from ..models.project_binding_verification_review_members import ProjectBindingVerificationReviewMembers
        from ..models.project_binding_verification_review_review_digests import (
            ProjectBindingVerificationReviewReviewDigests,
        )

        d = dict(src_dict)
        project = d.pop("project")

        environment = d.pop("environment")

        reviewed_at = datetime.datetime.fromisoformat(d.pop("reviewed_at"))

        ready = d.pop("ready")

        review_digest = d.pop("review_digest")

        review_digests = ProjectBindingVerificationReviewReviewDigests.from_dict(d.pop("review_digests"))

        manifest = CheckedProjectReleaseManifest.from_dict(d.pop("manifest"))

        object_storage_write = d.pop("object_storage_write")

        queue_publish = d.pop("queue_publish")

        object_storage_write_required = d.pop("object_storage_write_required")

        queue_publish_required = d.pop("queue_publish_required")

        members = ProjectBindingVerificationReviewMembers.from_dict(d.pop("members"))

        blockers = []
        _blockers = d.pop("blockers")
        for blockers_item_data in _blockers:
            blockers_item = BindingCheckFinding.from_dict(blockers_item_data)

            blockers.append(blockers_item)

        project_binding_verification_review = cls(
            project=project,
            environment=environment,
            reviewed_at=reviewed_at,
            ready=ready,
            review_digest=review_digest,
            review_digests=review_digests,
            manifest=manifest,
            object_storage_write=object_storage_write,
            queue_publish=queue_publish,
            object_storage_write_required=object_storage_write_required,
            queue_publish_required=queue_publish_required,
            members=members,
            blockers=blockers,
        )

        project_binding_verification_review.additional_properties = d
        return project_binding_verification_review

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
