from __future__ import annotations

from collections.abc import Mapping
from typing import TYPE_CHECKING, Any, TypeVar

from attrs import define as _attrs_define
from attrs import field as _attrs_field

if TYPE_CHECKING:
    from ..models.checked_project_release_manifest import CheckedProjectReleaseManifest


T = TypeVar("T", bound="ReviewProjectBindingVerificationRequest")


@_attrs_define
class ReviewProjectBindingVerificationRequest:
    """Project release selection to review without creating receipts or starting verification."""

    manifest: CheckedProjectReleaseManifest
    """Immutable candidate graph and expected routing and policy state. This manifest conveys no evidence or
    waiver."""
    object_storage_write: bool
    queue_publish: bool
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        manifest = self.manifest.to_dict()

        object_storage_write = self.object_storage_write

        queue_publish = self.queue_publish

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "manifest": manifest,
                "object_storage_write": object_storage_write,
                "queue_publish": queue_publish,
            }
        )

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        from ..models.checked_project_release_manifest import CheckedProjectReleaseManifest

        d = dict(src_dict)
        manifest = CheckedProjectReleaseManifest.from_dict(d.pop("manifest"))

        object_storage_write = d.pop("object_storage_write")

        queue_publish = d.pop("queue_publish")

        review_project_binding_verification_request = cls(
            manifest=manifest,
            object_storage_write=object_storage_write,
            queue_publish=queue_publish,
        )

        review_project_binding_verification_request.additional_properties = d
        return review_project_binding_verification_request

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
