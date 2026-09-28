from __future__ import annotations

from collections.abc import Mapping
from typing import Any, TypeVar

from attrs import define as _attrs_define

T = TypeVar("T", bound="RegisterEventSchemaRequest")


@_attrs_define
class RegisterEventSchemaRequest:
    """Immutable event schema registration request."""

    source: str
    type_: str
    version: str
    schema: Any
    """Draft 2020-12 JSON Schema"""

    def to_dict(self) -> dict[str, Any]:
        source = self.source

        type_ = self.type_

        version = self.version

        schema = self.schema

        field_dict: dict[str, Any] = {}

        field_dict.update(
            {
                "source": source,
                "type": type_,
                "version": version,
                "schema": schema,
            }
        )

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        source = d.pop("source")

        type_ = d.pop("type")

        version = d.pop("version")

        schema = d.pop("schema")

        register_event_schema_request = cls(
            source=source,
            type_=type_,
            version=version,
            schema=schema,
        )

        return register_event_schema_request
