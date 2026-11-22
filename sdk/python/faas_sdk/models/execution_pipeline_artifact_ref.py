from __future__ import annotations

from collections.abc import Mapping
from typing import Any, TypeVar

from attrs import define as _attrs_define
from attrs import field as _attrs_field

T = TypeVar("T", bound="ExecutionPipelineArtifactRef")


@_attrs_define
class ExecutionPipelineArtifactRef:
    """Checksum and locator for a retained file written by a completed step."""

    name: str
    size_bytes: int
    sha256: str
    url: str
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        name = self.name

        size_bytes = self.size_bytes

        sha256 = self.sha256

        url = self.url

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "name": name,
                "size_bytes": size_bytes,
                "sha256": sha256,
                "url": url,
            }
        )

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        name = d.pop("name")

        size_bytes = d.pop("size_bytes")

        sha256 = d.pop("sha256")

        url = d.pop("url")

        execution_pipeline_artifact_ref = cls(
            name=name,
            size_bytes=size_bytes,
            sha256=sha256,
            url=url,
        )

        execution_pipeline_artifact_ref.additional_properties = d
        return execution_pipeline_artifact_ref

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
