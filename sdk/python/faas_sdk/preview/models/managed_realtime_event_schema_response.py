from __future__ import annotations

import datetime
from collections.abc import Mapping
from typing import TYPE_CHECKING, Any, TypeVar, cast

from attrs import define as _attrs_define
from attrs import field as _attrs_field

if TYPE_CHECKING:
    from ..models.managed_realtime_event_schema_response_schema_type_0 import (
        ManagedRealtimeEventSchemaResponseSchemaType0,
    )


T = TypeVar("T", bound="ManagedRealtimeEventSchemaResponse")


@_attrs_define
class ManagedRealtimeEventSchemaResponse:
    """Stored JSON Schema used to validate retained channel events."""

    channel: str
    event_type: str
    version: int
    schema: bool | ManagedRealtimeEventSchemaResponseSchemaType0
    created_at: datetime.datetime
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        from ..models.managed_realtime_event_schema_response_schema_type_0 import (
            ManagedRealtimeEventSchemaResponseSchemaType0,
        )

        channel = self.channel

        event_type = self.event_type

        version = self.version

        schema: bool | dict[str, Any]
        if isinstance(self.schema, ManagedRealtimeEventSchemaResponseSchemaType0):
            schema = self.schema.to_dict()
        else:
            schema = self.schema

        created_at = self.created_at.isoformat()

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "channel": channel,
                "event_type": event_type,
                "version": version,
                "schema": schema,
                "created_at": created_at,
            }
        )

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        from ..models.managed_realtime_event_schema_response_schema_type_0 import (
            ManagedRealtimeEventSchemaResponseSchemaType0,
        )

        d = dict(src_dict)
        channel = d.pop("channel")

        event_type = d.pop("event_type")

        version = d.pop("version")

        def _parse_schema(data: object) -> bool | ManagedRealtimeEventSchemaResponseSchemaType0:
            try:
                if not isinstance(data, dict):
                    raise TypeError()
                schema_type_0 = ManagedRealtimeEventSchemaResponseSchemaType0.from_dict(data)

                return schema_type_0
            except (TypeError, ValueError, AttributeError, KeyError):
                pass
            return cast(bool | ManagedRealtimeEventSchemaResponseSchemaType0, data)

        schema = _parse_schema(d.pop("schema"))

        created_at = datetime.datetime.fromisoformat(d.pop("created_at"))

        managed_realtime_event_schema_response = cls(
            channel=channel,
            event_type=event_type,
            version=version,
            schema=schema,
            created_at=created_at,
        )

        managed_realtime_event_schema_response.additional_properties = d
        return managed_realtime_event_schema_response

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
