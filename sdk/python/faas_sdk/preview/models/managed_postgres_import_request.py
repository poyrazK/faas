from __future__ import annotations

from collections.abc import Mapping
from typing import TYPE_CHECKING, Any, TypeVar
from uuid import UUID

from attrs import define as _attrs_define

if TYPE_CHECKING:
    from ..models.managed_postgres_import_source import ManagedPostgresImportSource


T = TypeVar("T", bound="ManagedPostgresImportRequest")


@_attrs_define
class ManagedPostgresImportRequest:
    operation_id: UUID
    """Caller-generated lowercase UUID reused to recover or retry an import."""
    source: ManagedPostgresImportSource

    def to_dict(self) -> dict[str, Any]:
        operation_id = str(self.operation_id)

        source = self.source.to_dict()

        field_dict: dict[str, Any] = {}

        field_dict.update(
            {
                "operation_id": operation_id,
                "source": source,
            }
        )

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        from ..models.managed_postgres_import_source import ManagedPostgresImportSource

        d = dict(src_dict)
        operation_id = UUID(d.pop("operation_id"))

        source = ManagedPostgresImportSource.from_dict(d.pop("source"))

        managed_postgres_import_request = cls(
            operation_id=operation_id,
            source=source,
        )

        return managed_postgres_import_request
