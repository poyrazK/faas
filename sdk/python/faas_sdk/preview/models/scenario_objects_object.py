from __future__ import annotations

from collections.abc import Mapping
from typing import TYPE_CHECKING, Any, TypeVar

from attrs import define as _attrs_define
from attrs import field as _attrs_field

if TYPE_CHECKING:
    from ..models.scenario_objects_object_metadata import ScenarioObjectsObjectMetadata


T = TypeVar("T", bound="ScenarioObjectsObject")


@_attrs_define
class ScenarioObjectsObject:
    """Independent key, byte length, content digest and stable metadata without object content."""

    key: str
    size: int
    sha256: str
    content_type: str
    metadata: ScenarioObjectsObjectMetadata
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        key = self.key

        size = self.size

        sha256 = self.sha256

        content_type = self.content_type

        metadata = self.metadata.to_dict()

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "key": key,
                "size": size,
                "sha256": sha256,
                "content_type": content_type,
                "metadata": metadata,
            }
        )

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        from ..models.scenario_objects_object_metadata import ScenarioObjectsObjectMetadata

        d = dict(src_dict)
        key = d.pop("key")

        size = d.pop("size")

        sha256 = d.pop("sha256")

        content_type = d.pop("content_type")

        metadata = ScenarioObjectsObjectMetadata.from_dict(d.pop("metadata"))

        scenario_objects_object = cls(
            key=key,
            size=size,
            sha256=sha256,
            content_type=content_type,
            metadata=metadata,
        )

        scenario_objects_object.additional_properties = d
        return scenario_objects_object

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
