from __future__ import annotations

from collections.abc import Mapping
from typing import Any, TypeVar

from attrs import define as _attrs_define
from attrs import field as _attrs_field

T = TypeVar("T", bound="IssueSourceMapArtifact")


@_attrs_define
class IssueSourceMapArtifact:
    """One generated bundle and its bounded source-map metadata, without embedded source contents."""

    generated_file: str
    """Portable relative bundle path from the Node build manifest."""
    sha256: str
    """Producer-supplied bundle digest; the server does not retrieve bundle bytes to independently verify it."""
    payload: str
    """Flat Source Map v3 JSON; byte and decoded limits are enforced before persistence."""
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        generated_file = self.generated_file

        sha256 = self.sha256

        payload = self.payload

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "generatedFile": generated_file,
                "sha256": sha256,
                "payload": payload,
            }
        )

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        generated_file = d.pop("generatedFile")

        sha256 = d.pop("sha256")

        payload = d.pop("payload")

        issue_source_map_artifact = cls(
            generated_file=generated_file,
            sha256=sha256,
            payload=payload,
        )

        issue_source_map_artifact.additional_properties = d
        return issue_source_map_artifact

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
