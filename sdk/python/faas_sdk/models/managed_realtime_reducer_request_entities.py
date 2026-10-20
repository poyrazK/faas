from __future__ import annotations

from collections.abc import Mapping
from typing import TYPE_CHECKING, Any, TypeVar

from attrs import define as _attrs_define
from attrs import field as _attrs_field

if TYPE_CHECKING:
    from ..models.managed_realtime_reducer_request_entities_additional_property import (
        ManagedRealtimeReducerRequestEntitiesAdditionalProperty,
    )


T = TypeVar("T", bound="ManagedRealtimeReducerRequestEntities")


@_attrs_define
class ManagedRealtimeReducerRequestEntities:
    """Seed entities, at most 64 KiB encoded. Keys are 1..128 UTF-8 bytes."""

    additional_properties: dict[str, ManagedRealtimeReducerRequestEntitiesAdditionalProperty] = _attrs_field(
        init=False, factory=dict
    )

    def to_dict(self) -> dict[str, Any]:

        field_dict: dict[str, Any] = {}
        for prop_name, prop in self.additional_properties.items():
            field_dict[prop_name] = prop.to_dict()

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        from ..models.managed_realtime_reducer_request_entities_additional_property import (
            ManagedRealtimeReducerRequestEntitiesAdditionalProperty,
        )

        d = dict(src_dict)
        managed_realtime_reducer_request_entities = cls()

        additional_properties = {}
        for prop_name, prop_dict in d.items():
            additional_property = ManagedRealtimeReducerRequestEntitiesAdditionalProperty.from_dict(prop_dict)

            additional_properties[prop_name] = additional_property

        managed_realtime_reducer_request_entities.additional_properties = additional_properties
        return managed_realtime_reducer_request_entities

    @property
    def additional_keys(self) -> list[str]:
        return list(self.additional_properties.keys())

    def __getitem__(self, key: str) -> ManagedRealtimeReducerRequestEntitiesAdditionalProperty:
        return self.additional_properties[key]

    def __setitem__(self, key: str, value: ManagedRealtimeReducerRequestEntitiesAdditionalProperty) -> None:
        self.additional_properties[key] = value

    def __delitem__(self, key: str) -> None:
        del self.additional_properties[key]

    def __contains__(self, key: str) -> bool:
        return key in self.additional_properties
