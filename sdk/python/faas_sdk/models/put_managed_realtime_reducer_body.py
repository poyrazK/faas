from __future__ import annotations

from collections.abc import Mapping
from typing import TYPE_CHECKING, Any, TypeVar

from attrs import define as _attrs_define
from attrs import field as _attrs_field

if TYPE_CHECKING:
    from ..models.put_managed_realtime_reducer_body_entities import PutManagedRealtimeReducerBodyEntities


T = TypeVar("T", bound="PutManagedRealtimeReducerBody")


@_attrs_define
class PutManagedRealtimeReducerBody:
    sequence: int
    entities: PutManagedRealtimeReducerBodyEntities
    """Seed entities, at most 64 KiB encoded. Keys are 1..128 UTF-8 bytes."""
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        sequence = self.sequence

        entities = self.entities.to_dict()

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "sequence": sequence,
                "entities": entities,
            }
        )

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        from ..models.put_managed_realtime_reducer_body_entities import PutManagedRealtimeReducerBodyEntities

        d = dict(src_dict)
        sequence = d.pop("sequence")

        entities = PutManagedRealtimeReducerBodyEntities.from_dict(d.pop("entities"))

        put_managed_realtime_reducer_body = cls(
            sequence=sequence,
            entities=entities,
        )

        put_managed_realtime_reducer_body.additional_properties = d
        return put_managed_realtime_reducer_body

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
