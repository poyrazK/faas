from __future__ import annotations

from collections.abc import Mapping
from typing import TYPE_CHECKING, Any, TypeVar

from attrs import define as _attrs_define
from attrs import field as _attrs_field

if TYPE_CHECKING:
    from ..models.scenario_objects_data_metadata import ScenarioObjectsDataMetadata


T = TypeVar("T", bound="ScenarioObjectsData")


@_attrs_define
class ScenarioObjectsData:
    """Bounded object fixture input; raw content is never included in evidence responses."""

    key: str
    body: str
    """Base64-encoded bytes, at most 65536 decoded bytes per object and 262144 decoded bytes in total."""
    content_type: str
    metadata: ScenarioObjectsDataMetadata
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        key = self.key

        body = self.body

        content_type = self.content_type

        metadata = self.metadata.to_dict()

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "key": key,
                "body": body,
                "content_type": content_type,
                "metadata": metadata,
            }
        )

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        from ..models.scenario_objects_data_metadata import ScenarioObjectsDataMetadata

        d = dict(src_dict)
        key = d.pop("key")

        body = d.pop("body")

        content_type = d.pop("content_type")

        metadata = ScenarioObjectsDataMetadata.from_dict(d.pop("metadata"))

        scenario_objects_data = cls(
            key=key,
            body=body,
            content_type=content_type,
            metadata=metadata,
        )

        scenario_objects_data.additional_properties = d
        return scenario_objects_data

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
