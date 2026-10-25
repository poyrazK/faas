from __future__ import annotations

from collections.abc import Mapping
from typing import TYPE_CHECKING, Any, TypeVar

from attrs import define as _attrs_define

if TYPE_CHECKING:
    from ..models.checked_project_release_manifest import CheckedProjectReleaseManifest


T = TypeVar("T", bound="StageProjectReleaseRequest")


@_attrs_define
class StageProjectReleaseRequest:
    """Pinned project release manifest and lifetime for temporary candidate service staging."""

    manifest: CheckedProjectReleaseManifest
    """Immutable candidate graph and expected routing and policy state. This manifest conveys no evidence or
    waiver."""
    ttl_seconds: int
    """Temporary billable capacity lease; repeating the same active stage does not extend it."""

    def to_dict(self) -> dict[str, Any]:
        manifest = self.manifest.to_dict()

        ttl_seconds = self.ttl_seconds

        field_dict: dict[str, Any] = {}

        field_dict.update(
            {
                "manifest": manifest,
                "ttl_seconds": ttl_seconds,
            }
        )

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        from ..models.checked_project_release_manifest import CheckedProjectReleaseManifest

        d = dict(src_dict)
        manifest = CheckedProjectReleaseManifest.from_dict(d.pop("manifest"))

        ttl_seconds = d.pop("ttl_seconds")

        stage_project_release_request = cls(
            manifest=manifest,
            ttl_seconds=ttl_seconds,
        )

        return stage_project_release_request
