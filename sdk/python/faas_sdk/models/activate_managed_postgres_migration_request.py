from __future__ import annotations

from collections.abc import Mapping
from typing import Any, TypeVar
from uuid import UUID

from attrs import define as _attrs_define

from ..types import UNSET, Unset

T = TypeVar("T", bound="ActivateManagedPostgresMigrationRequest")


@_attrs_define
class ActivateManagedPostgresMigrationRequest:
    source_database_id: UUID
    target_database_id: UUID
    operation_id: UUID
    verification_id: UUID
    """UUID of the current write pause and stable switch ID."""
    source_writes_paused: bool
    """The operator confirms source writers are paused and drained. Must be true."""
    scope: str | Unset = "default"

    def to_dict(self) -> dict[str, Any]:
        source_database_id = str(self.source_database_id)

        target_database_id = str(self.target_database_id)

        operation_id = str(self.operation_id)

        verification_id = str(self.verification_id)

        source_writes_paused = self.source_writes_paused

        scope = self.scope

        field_dict: dict[str, Any] = {}

        field_dict.update(
            {
                "source_database_id": source_database_id,
                "target_database_id": target_database_id,
                "operation_id": operation_id,
                "verification_id": verification_id,
                "source_writes_paused": source_writes_paused,
            }
        )
        if scope is not UNSET:
            field_dict["scope"] = scope

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        source_database_id = UUID(d.pop("source_database_id"))

        target_database_id = UUID(d.pop("target_database_id"))

        operation_id = UUID(d.pop("operation_id"))

        verification_id = UUID(d.pop("verification_id"))

        source_writes_paused = d.pop("source_writes_paused")

        scope = d.pop("scope", UNSET)

        activate_managed_postgres_migration_request = cls(
            source_database_id=source_database_id,
            target_database_id=target_database_id,
            operation_id=operation_id,
            verification_id=verification_id,
            source_writes_paused=source_writes_paused,
            scope=scope,
        )

        return activate_managed_postgres_migration_request
