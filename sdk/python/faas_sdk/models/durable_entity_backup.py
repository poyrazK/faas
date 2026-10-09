from __future__ import annotations

import datetime
from collections.abc import Mapping
from typing import TYPE_CHECKING, Any, TypeVar

from attrs import define as _attrs_define

if TYPE_CHECKING:
    from ..models.durable_entity_state_export import DurableEntityStateExport


T = TypeVar("T", bound="DurableEntityBackup")


@_attrs_define
class DurableEntityBackup:
    captured_at: datetime.datetime
    """UTC hourly slot start."""
    export: DurableEntityStateExport

    def to_dict(self) -> dict[str, Any]:
        captured_at = self.captured_at.isoformat()

        export = self.export.to_dict()

        field_dict: dict[str, Any] = {}

        field_dict.update(
            {
                "captured_at": captured_at,
                "export": export,
            }
        )

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        from ..models.durable_entity_state_export import DurableEntityStateExport

        d = dict(src_dict)
        captured_at = datetime.datetime.fromisoformat(d.pop("captured_at"))

        export = DurableEntityStateExport.from_dict(d.pop("export"))

        durable_entity_backup = cls(
            captured_at=captured_at,
            export=export,
        )

        return durable_entity_backup
