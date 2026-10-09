from __future__ import annotations

import datetime
from collections.abc import Mapping
from typing import Any, TypeVar

from attrs import define as _attrs_define

T = TypeVar("T", bound="DurableEntityBackupInfo")


@_attrs_define
class DurableEntityBackupInfo:
    id: str
    captured_at: datetime.datetime
    version: int

    def to_dict(self) -> dict[str, Any]:
        id = self.id

        captured_at = self.captured_at.isoformat()

        version = self.version

        field_dict: dict[str, Any] = {}

        field_dict.update(
            {
                "id": id,
                "captured_at": captured_at,
                "version": version,
            }
        )

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        id = d.pop("id")

        captured_at = datetime.datetime.fromisoformat(d.pop("captured_at"))

        version = d.pop("version")

        durable_entity_backup_info = cls(
            id=id,
            captured_at=captured_at,
            version=version,
        )

        return durable_entity_backup_info
