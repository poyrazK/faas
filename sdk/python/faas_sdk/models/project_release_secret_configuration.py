from __future__ import annotations

from collections.abc import Mapping
from typing import TYPE_CHECKING, Any, TypeVar

from attrs import define as _attrs_define

if TYPE_CHECKING:
    from ..models.project_release_secret_version_map import ProjectReleaseSecretVersionMap


T = TypeVar("T", bound="ProjectReleaseSecretConfiguration")


@_attrs_define
class ProjectReleaseSecretConfiguration:
    """Candidate and baseline immutable secret version references bound to an atomic release."""

    versions: ProjectReleaseSecretVersionMap
    """Workload names mapped to complete secret name and immutable version ID snapshots; empty maps remove secrets
    from that candidate."""
    previous_versions: ProjectReleaseSecretVersionMap
    """Workload names mapped to complete secret name and immutable version ID snapshots; empty maps remove secrets
    from that candidate."""

    def to_dict(self) -> dict[str, Any]:
        versions = self.versions.to_dict()

        previous_versions = self.previous_versions.to_dict()

        field_dict: dict[str, Any] = {}

        field_dict.update(
            {
                "versions": versions,
                "previous_versions": previous_versions,
            }
        )

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        from ..models.project_release_secret_version_map import ProjectReleaseSecretVersionMap

        d = dict(src_dict)
        versions = ProjectReleaseSecretVersionMap.from_dict(d.pop("versions"))

        previous_versions = ProjectReleaseSecretVersionMap.from_dict(d.pop("previous_versions"))

        project_release_secret_configuration = cls(
            versions=versions,
            previous_versions=previous_versions,
        )

        return project_release_secret_configuration
