from __future__ import annotations

from collections.abc import Mapping
from typing import TYPE_CHECKING, Any, TypeVar

from attrs import define as _attrs_define

from ..models.durable_entity_state_export_format import (
    DurableEntityStateExportFormat,
    check_durable_entity_state_export_format,
)

if TYPE_CHECKING:
    from ..models.durable_entity_scope import DurableEntityScope


T = TypeVar("T", bound="DurableEntityStateExport")


@_attrs_define
class DurableEntityStateExport:
    format_: DurableEntityStateExportFormat
    entity: DurableEntityScope
    version: int
    data: Any
    """Opaque application JSON, including any application schema envelope."""
    checksum: str

    def to_dict(self) -> dict[str, Any]:
        format_: int = self.format_

        entity = self.entity.to_dict()

        version = self.version

        data = self.data

        checksum = self.checksum

        field_dict: dict[str, Any] = {}

        field_dict.update(
            {
                "format": format_,
                "entity": entity,
                "version": version,
                "data": data,
                "checksum": checksum,
            }
        )

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        from ..models.durable_entity_scope import DurableEntityScope

        d = dict(src_dict)
        format_ = check_durable_entity_state_export_format(d.pop("format"))

        entity = DurableEntityScope.from_dict(d.pop("entity"))

        version = d.pop("version")

        data = d.pop("data")

        checksum = d.pop("checksum")

        durable_entity_state_export = cls(
            format_=format_,
            entity=entity,
            version=version,
            data=data,
            checksum=checksum,
        )

        return durable_entity_state_export
