from __future__ import annotations

from collections.abc import Mapping
from typing import TYPE_CHECKING, Any, TypeVar, cast

from attrs import define as _attrs_define
from attrs import field as _attrs_field

if TYPE_CHECKING:
    from ..models.managed_realtime_event_schema_request_schema_type_0 import (
        ManagedRealtimeEventSchemaRequestSchemaType0,
    )


T = TypeVar("T", bound="ManagedRealtimeEventSchemaRequest")


@_attrs_define
class ManagedRealtimeEventSchemaRequest:
    """JSON Schema document for one channel event type and version."""

    schema: bool | ManagedRealtimeEventSchemaRequestSchemaType0
    """JSON Schema Draft 2020-12, at most 16384 encoded bytes; external resources are unsupported."""
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        from ..models.managed_realtime_event_schema_request_schema_type_0 import (
            ManagedRealtimeEventSchemaRequestSchemaType0,
        )

        schema: bool | dict[str, Any]
        if isinstance(self.schema, ManagedRealtimeEventSchemaRequestSchemaType0):
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
        from ..models.managed_realtime_event_schema_request_schema_type_0 import (
            ManagedRealtimeEventSchemaRequestSchemaType0,
        )

        d = dict(src_dict)

        def _parse_schema(data: object) -> bool | ManagedRealtimeEventSchemaRequestSchemaType0:
            try:
                if not isinstance(data, dict):
                    raise TypeError()
                schema_type_0 = ManagedRealtimeEventSchemaRequestSchemaType0.from_dict(data)

                return schema_type_0
            except (TypeError, ValueError, AttributeError, KeyError):
                pass
            return cast(bool | ManagedRealtimeEventSchemaRequestSchemaType0, data)

        schema = _parse_schema(d.pop("schema"))

        managed_realtime_event_schema_request = cls(
            schema=schema,
        )

        managed_realtime_event_schema_request.additional_properties = d
        return managed_realtime_event_schema_request

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
