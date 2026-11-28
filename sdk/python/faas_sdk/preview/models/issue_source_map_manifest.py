from __future__ import annotations

from collections.abc import Mapping
from typing import TYPE_CHECKING, Any, TypeVar

from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..models.issue_source_map_manifest_version import (
    IssueSourceMapManifestVersion,
    check_issue_source_map_manifest_version,
)

if TYPE_CHECKING:
    from ..models.issue_source_map_artifact import IssueSourceMapArtifact


T = TypeVar("T", bound="IssueSourceMapManifest")


@_attrs_define
class IssueSourceMapManifest:
    """Portable source-map manifest with exact generated-bundle digests and original source locations."""

    version: IssueSourceMapManifestVersion
    artifacts: list[IssueSourceMapArtifact]
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        version: int = self.version

        artifacts = []
        for artifacts_item_data in self.artifacts:
            artifacts_item = artifacts_item_data.to_dict()
            artifacts.append(artifacts_item)

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "version": version,
                "artifacts": artifacts,
            }
        )

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        from ..models.issue_source_map_artifact import IssueSourceMapArtifact

        d = dict(src_dict)
        version = check_issue_source_map_manifest_version(d.pop("version"))

        artifacts = []
        _artifacts = d.pop("artifacts")
        for artifacts_item_data in _artifacts:
            artifacts_item = IssueSourceMapArtifact.from_dict(artifacts_item_data)

            artifacts.append(artifacts_item)

        issue_source_map_manifest = cls(
            version=version,
            artifacts=artifacts,
        )

        issue_source_map_manifest.additional_properties = d
        return issue_source_map_manifest

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
