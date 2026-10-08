from __future__ import annotations

from collections.abc import Mapping
from typing import TYPE_CHECKING, Any, TypeVar, cast

from attrs import define as _attrs_define
from attrs import field as _attrs_field

if TYPE_CHECKING:
    from ..models.put_managed_realtime_event_schema_body_schema_type_0 import (
        PutManagedRealtimeEventSchemaBodySchemaType0,
    )


T = TypeVar("T", bound="PutManagedRealtimeEventSchemaBody")


@_attrs_define
class PutManagedRealtimeEventSchemaBody:
    schema: bool | PutManagedRealtimeEventSchemaBodySchemaType0
    """JSON Schema Draft 2020-12, at most 16384 encoded bytes; external resources are unsupported."""
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        from ..models.put_managed_realtime_event_schema_body_schema_type_0 import (
            PutManagedRealtimeEventSchemaBodySchemaType0,
        )

        schema: bool | dict[str, Any]
        if isinstance(self.schema, PutManagedRealtimeEventSchemaBodySchemaType0):
            schema = self.schema.to_dict()
        else:
            schema = self.schema

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "schema": schema,
            }
        )

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        from ..models.put_managed_realtime_event_schema_body_schema_type_0 import (
            PutManagedRealtimeEventSchemaBodySchemaType0,
        )

        d = dict(src_dict)

        def _parse_schema(data: object) -> bool | PutManagedRealtimeEventSchemaBodySchemaType0:
            try:
                if not isinstance(data, dict):
                    raise TypeError()
                schema_type_0 = PutManagedRealtimeEventSchemaBodySchemaType0.from_dict(data)

                return schema_type_0
            except (TypeError, ValueError, AttributeError, KeyError):
                pass
            return cast(bool | PutManagedRealtimeEventSchemaBodySchemaType0, data)

        schema = _parse_schema(d.pop("schema"))

        put_managed_realtime_event_schema_body = cls(
            schema=schema,
        )

        put_managed_realtime_event_schema_body.additional_properties = d
        return put_managed_realtime_event_schema_body

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
