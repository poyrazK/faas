from __future__ import annotations

from collections.abc import Mapping
from typing import TYPE_CHECKING, Any, TypeVar

from attrs import define as _attrs_define
from attrs import field as _attrs_field

if TYPE_CHECKING:
    from ..models.issue_source_map_manifest import IssueSourceMapManifest


T = TypeVar("T", bound="PublishIssueSourceMapsRequest")


@_attrs_define
class PublishIssueSourceMapsRequest:
    """Deployment source-map publication request containing the deployed directory and portable manifest."""

    directory: str
    """Absolute deployed directory corresponding to the manifest scan root."""
    manifest: IssueSourceMapManifest
    """Portable source-map manifest with exact generated-bundle digests and original source locations."""
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        directory = self.directory

        manifest = self.manifest.to_dict()

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "directory": directory,
                "manifest": manifest,
            }
        )

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        from ..models.issue_source_map_manifest import IssueSourceMapManifest

        d = dict(src_dict)
        directory = d.pop("directory")

        manifest = IssueSourceMapManifest.from_dict(d.pop("manifest"))

        publish_issue_source_maps_request = cls(
            directory=directory,
            manifest=manifest,
        )

        publish_issue_source_maps_request.additional_properties = d
        return publish_issue_source_maps_request

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
