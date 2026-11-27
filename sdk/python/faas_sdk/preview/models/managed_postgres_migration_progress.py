from __future__ import annotations

from collections.abc import Mapping
from typing import TYPE_CHECKING, Any, TypeVar
from uuid import UUID

from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..models.managed_postgres_migration_progress_state import (
    ManagedPostgresMigrationProgressState,
    check_managed_postgres_migration_progress_state,
)
from ..types import UNSET, Unset

if TYPE_CHECKING:
    from ..models.managed_postgres_migration_preparation import ManagedPostgresMigrationPreparation
    from ..models.managed_postgres_migration_verification import ManagedPostgresMigrationVerification


T = TypeVar("T", bound="ManagedPostgresMigrationProgress")


@_attrs_define
class ManagedPostgresMigrationProgress:
    """Latest observed progress for the native logical subscriber."""

    state: ManagedPostgresMigrationProgressState
    tables: int
    tables_ready: int
    apply_lag_bytes: int
    """Observed source WAL bytes beyond the subscriber's confirmed position."""
    retained_wal_bytes: int
    """Observed source WAL retained by the main slot."""
    operation_id: UUID | Unset = UNSET
    reason: str | Unset = UNSET
    preparation: ManagedPostgresMigrationPreparation | Unset = UNSET
    """Schema preparation on the fenced target before the final source write pause."""
    verification: ManagedPostgresMigrationVerification | Unset = UNSET
    """Durable replay evidence for one operator-declared source write pause."""
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        state: str = self.state

        tables = self.tables

        tables_ready = self.tables_ready

        apply_lag_bytes = self.apply_lag_bytes

        retained_wal_bytes = self.retained_wal_bytes

        operation_id: str | Unset = UNSET
        if not isinstance(self.operation_id, Unset):
            operation_id = str(self.operation_id)

        reason = self.reason

        preparation: dict[str, Any] | Unset = UNSET
        if not isinstance(self.preparation, Unset):
            preparation = self.preparation.to_dict()

        verification: dict[str, Any] | Unset = UNSET
        if not isinstance(self.verification, Unset):
            verification = self.verification.to_dict()

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "state": state,
                "tables": tables,
                "tables_ready": tables_ready,
                "apply_lag_bytes": apply_lag_bytes,
                "retained_wal_bytes": retained_wal_bytes,
            }
        )
        if operation_id is not UNSET:
            field_dict["operation_id"] = operation_id
        if reason is not UNSET:
            field_dict["reason"] = reason
        if preparation is not UNSET:
            field_dict["preparation"] = preparation
        if verification is not UNSET:
            field_dict["verification"] = verification

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        from ..models.managed_postgres_migration_preparation import ManagedPostgresMigrationPreparation
        from ..models.managed_postgres_migration_verification import ManagedPostgresMigrationVerification

        d = dict(src_dict)
        state = check_managed_postgres_migration_progress_state(d.pop("state"))

        tables = d.pop("tables")

        tables_ready = d.pop("tables_ready")

        apply_lag_bytes = d.pop("apply_lag_bytes")

        retained_wal_bytes = d.pop("retained_wal_bytes")

        _operation_id = d.pop("operation_id", UNSET)
        operation_id: UUID | Unset
        if isinstance(_operation_id, Unset):
            operation_id = UNSET
        else:
            operation_id = UUID(_operation_id)

        reason = d.pop("reason", UNSET)

        _preparation = d.pop("preparation", UNSET)
        preparation: ManagedPostgresMigrationPreparation | Unset
        if isinstance(_preparation, Unset):
            preparation = UNSET
        else:
            preparation = ManagedPostgresMigrationPreparation.from_dict(_preparation)

        _verification = d.pop("verification", UNSET)
        verification: ManagedPostgresMigrationVerification | Unset
        if isinstance(_verification, Unset):
            verification = UNSET
        else:
            verification = ManagedPostgresMigrationVerification.from_dict(_verification)

        managed_postgres_migration_progress = cls(
            state=state,
            tables=tables,
            tables_ready=tables_ready,
            apply_lag_bytes=apply_lag_bytes,
            retained_wal_bytes=retained_wal_bytes,
            operation_id=operation_id,
            reason=reason,
            preparation=preparation,
            verification=verification,
        )

        managed_postgres_migration_progress.additional_properties = d
        return managed_postgres_migration_progress

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
