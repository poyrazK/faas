from __future__ import annotations

import datetime
from collections.abc import Mapping
from typing import Any, TypeVar
from uuid import UUID

from attrs import define as _attrs_define

T = TypeVar("T", bound="DataConnection")


@_attrs_define
class DataConnection:
    """Credential-free source identity retained by the pipeline catalog."""

    id: UUID
    name: str
    revision: int
    created_at: datetime.datetime

    def to_dict(self) -> dict[str, Any]:
        id = str(self.id)

        name = self.name

        revision = self.revision

        created_at = self.created_at.isoformat()

        field_dict: dict[str, Any] = {}

        field_dict.update(
            {
                "id": id,
                "name": name,
                "revision": revision,
                "created_at": created_at,
            }
        )

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        id = UUID(d.pop("id"))

        name = d.pop("name")

        revision = d.pop("revision")

        created_at = datetime.datetime.fromisoformat(d.pop("created_at"))

        data_connection = cls(
            id=id,
            name=name,
            revision=revision,
            created_at=created_at,
        )

        return data_connection
