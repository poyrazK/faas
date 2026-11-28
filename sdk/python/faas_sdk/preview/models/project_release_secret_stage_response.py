from __future__ import annotations

from collections.abc import Mapping
from typing import TYPE_CHECKING, Any, TypeVar

from attrs import define as _attrs_define

if TYPE_CHECKING:
    from ..models.project_release_secret_version_map import ProjectReleaseSecretVersionMap


T = TypeVar("T", bound="ProjectReleaseSecretStageResponse")


@_attrs_define
class ProjectReleaseSecretStageResponse:
    """Stable staging identity and version references returned after encryption."""

    request_id: str
    versions: ProjectReleaseSecretVersionMap
    """Workload names mapped to complete secret name and immutable version ID snapshots; empty maps remove secrets
    from that candidate."""

    def to_dict(self) -> dict[str, Any]:
        request_id = self.request_id

        versions = self.versions.to_dict()

        field_dict: dict[str, Any] = {}

        field_dict.update(
            {
                "request_id": request_id,
                "versions": versions,
            }
        )

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        from ..models.project_release_secret_version_map import ProjectReleaseSecretVersionMap

        d = dict(src_dict)
        request_id = d.pop("request_id")

        versions = ProjectReleaseSecretVersionMap.from_dict(d.pop("versions"))

        project_release_secret_stage_response = cls(
            request_id=request_id,
            versions=versions,
        )

        return project_release_secret_stage_response
