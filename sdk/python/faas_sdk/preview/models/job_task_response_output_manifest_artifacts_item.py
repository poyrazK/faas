from __future__ import annotations

from collections.abc import Mapping
from typing import Any, TypeVar

from attrs import define as _attrs_define
from attrs import field as _attrs_field

T = TypeVar("T", bound="JobTaskResponseOutputManifestArtifactsItem")


@_attrs_define
class JobTaskResponseOutputManifestArtifactsItem:
    name: str
    uri: str
    size_bytes: int
    sha256: str
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        name = self.name

        uri = self.uri

        size_bytes = self.size_bytes

        sha256 = self.sha256

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "name": name,
                "uri": uri,
                "size_bytes": size_bytes,
                "sha256": sha256,
            }
        )

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        name = d.pop("name")

        uri = d.pop("uri")

        size_bytes = d.pop("size_bytes")

        sha256 = d.pop("sha256")

        job_task_response_output_manifest_artifacts_item = cls(
            name=name,
            uri=uri,
            size_bytes=size_bytes,
            sha256=sha256,
        )

        job_task_response_output_manifest_artifacts_item.additional_properties = d
        return job_task_response_output_manifest_artifacts_item

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
