from __future__ import annotations

from collections.abc import Mapping
from typing import TYPE_CHECKING, Any, TypeVar
from uuid import UUID

from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..types import UNSET, Unset

if TYPE_CHECKING:
    from ..models.checked_project_release_manifest import CheckedProjectReleaseManifest
    from ..models.create_project_binding_verification_run_request_review_digests import (
        CreateProjectBindingVerificationRunRequestReviewDigests,
    )


T = TypeVar("T", bound="CreateProjectBindingVerificationRunRequest")


@_attrs_define
class CreateProjectBindingVerificationRunRequest:
    """Reviewed project selection, consent and shared concurrency for creating a durable verification graph."""

    id: UUID
    manifest: CheckedProjectReleaseManifest
    """Immutable candidate graph and expected routing and policy state. This manifest conveys no evidence or
    waiver."""
    review_digests: CreateProjectBindingVerificationRunRequestReviewDigests
    ttl_seconds: int
    object_storage_write: bool
    queue_publish: bool
    max_concurrency: int | Unset = 2
    """Requested shared graph admission budget; defaults to two and applies across all project members."""
    review_digest: str | Unset = UNSET
    """Optional aggregate digest issued by graph review; when supplied, manifest, member digests and consent must
    match."""
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        id = str(self.id)

        manifest = self.manifest.to_dict()

        review_digests = self.review_digests.to_dict()

        ttl_seconds = self.ttl_seconds

        object_storage_write = self.object_storage_write

        queue_publish = self.queue_publish

        max_concurrency = self.max_concurrency

        review_digest = self.review_digest

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "id": id,
                "manifest": manifest,
                "review_digests": review_digests,
                "ttl_seconds": ttl_seconds,
                "object_storage_write": object_storage_write,
                "queue_publish": queue_publish,
            }
        )
        if max_concurrency is not UNSET:
            field_dict["max_concurrency"] = max_concurrency
        if review_digest is not UNSET:
            field_dict["review_digest"] = review_digest

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        from ..models.checked_project_release_manifest import CheckedProjectReleaseManifest
        from ..models.create_project_binding_verification_run_request_review_digests import (
            CreateProjectBindingVerificationRunRequestReviewDigests,
        )

        d = dict(src_dict)
        id = UUID(d.pop("id"))

        manifest = CheckedProjectReleaseManifest.from_dict(d.pop("manifest"))

        review_digests = CreateProjectBindingVerificationRunRequestReviewDigests.from_dict(d.pop("review_digests"))

        ttl_seconds = d.pop("ttl_seconds")

        object_storage_write = d.pop("object_storage_write")

        queue_publish = d.pop("queue_publish")

        max_concurrency = d.pop("max_concurrency", UNSET)

        review_digest = d.pop("review_digest", UNSET)

        create_project_binding_verification_run_request = cls(
            id=id,
            manifest=manifest,
            review_digests=review_digests,
            ttl_seconds=ttl_seconds,
            object_storage_write=object_storage_write,
            queue_publish=queue_publish,
            max_concurrency=max_concurrency,
            review_digest=review_digest,
        )

        create_project_binding_verification_run_request.additional_properties = d
        return create_project_binding_verification_run_request

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
