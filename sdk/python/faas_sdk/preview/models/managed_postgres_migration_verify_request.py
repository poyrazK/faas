from __future__ import annotations

from collections.abc import Mapping
from typing import TYPE_CHECKING, Any, TypeVar
from uuid import UUID

from attrs import define as _attrs_define

if TYPE_CHECKING:
    from ..models.managed_postgres_import_source import ManagedPostgresImportSource


T = TypeVar("T", bound="ManagedPostgresMigrationVerifyRequest")


@_attrs_define
class ManagedPostgresMigrationVerifyRequest:
    """Request to verify migration replay after source writes have paused."""

    operation_id: UUID
    source: ManagedPostgresImportSource
    """Connection details for a PostgreSQL source to inspect or import."""
    verification_id: UUID
    """Reuse the UUID during the same continuous source write pause."""
    source_writes_paused: bool
    """The operator confirms that source writers are paused and drained. Must be true."""

    def to_dict(self) -> dict[str, Any]:
        operation_id = str(self.operation_id)

        source = self.source.to_dict()

        verification_id = str(self.verification_id)

        source_writes_paused = self.source_writes_paused

        field_dict: dict[str, Any] = {}

        field_dict.update(
            {
                "operation_id": operation_id,
                "source": source,
                "verification_id": verification_id,
                "source_writes_paused": source_writes_paused,
            }
        )

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        from ..models.managed_postgres_import_source import ManagedPostgresImportSource

        d = dict(src_dict)
        operation_id = UUID(d.pop("operation_id"))

        source = ManagedPostgresImportSource.from_dict(d.pop("source"))

        verification_id = UUID(d.pop("verification_id"))

        source_writes_paused = d.pop("source_writes_paused")

        managed_postgres_migration_verify_request = cls(
            operation_id=operation_id,
            source=source,
            verification_id=verification_id,
            source_writes_paused=source_writes_paused,
        )

        return managed_postgres_migration_verify_request
