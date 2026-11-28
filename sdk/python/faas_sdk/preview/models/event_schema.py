from __future__ import annotations

import datetime
from collections.abc import Mapping
from typing import Any, TypeVar
from uuid import UUID

from attrs import define as _attrs_define
from attrs import field as _attrs_field

T = TypeVar("T", bound="EventSchema")


@_attrs_define
class EventSchema:
    """One registered account-scoped event schema version."""

    account_id: UUID
    source: str
    type_: str
    version: str
    schema: Any
    """Immutable Draft 2020-12 JSON Schema."""
    created_at: datetime.datetime
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        account_id = str(self.account_id)

        source = self.source

        type_ = self.type_

        version = self.version

        schema = self.schema

        created_at = self.created_at.isoformat()

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "account_id": account_id,
                "source": source,
                "type": type_,
                "version": version,
                "schema": schema,
                "created_at": created_at,
            }
        )

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        account_id = UUID(d.pop("account_id"))

        source = d.pop("source")

        type_ = d.pop("type")

        version = d.pop("version")

        schema = d.pop("schema")

        created_at = datetime.datetime.fromisoformat(d.pop("created_at"))

        event_schema = cls(
            account_id=account_id,
            source=source,
            type_=type_,
            version=version,
            schema=schema,
            created_at=created_at,
        )

        event_schema.additional_properties = d
        return event_schema

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
