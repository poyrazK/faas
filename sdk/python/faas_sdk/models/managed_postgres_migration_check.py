from __future__ import annotations

from collections.abc import Mapping
from typing import Any, TypeVar, cast

from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..models.managed_postgres_migration_check_outcome import (
    ManagedPostgresMigrationCheckOutcome,
    check_managed_postgres_migration_check_outcome,
)
from ..types import UNSET, Unset

T = TypeVar("T", bound="ManagedPostgresMigrationCheck")


@_attrs_define
class ManagedPostgresMigrationCheck:
    """Observed PostgreSQL prerequisites for a logical migration. A compatible result covers these specific checks."""

    outcome: ManagedPostgresMigrationCheckOutcome
    table_count: int
    sequence_count: int
    """Sequences that need synchronization before opening a replicated target to writes."""
    source_wal_limit_bytes: int
    """A negative value indicates unlimited WAL retention."""
    free_source_slots: int
    free_source_senders: int
    reasons: list[str] | Unset = UNSET
    source_major: int | Unset = UNSET
    target_major: int | Unset = UNSET
    unsupported_table_count: int | Unset = UNSET
    unsupported_tables: list[str] | Unset = UNSET
    source_bytes: int | Unset = UNSET
    target_free_bytes: int | Unset = UNSET
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        outcome: str = self.outcome

        table_count = self.table_count

        sequence_count = self.sequence_count

        source_wal_limit_bytes = self.source_wal_limit_bytes

        free_source_slots = self.free_source_slots

        free_source_senders = self.free_source_senders

        reasons: list[str] | Unset = UNSET
        if not isinstance(self.reasons, Unset):
            reasons = self.reasons

        source_major = self.source_major

        target_major = self.target_major

        unsupported_table_count = self.unsupported_table_count

        unsupported_tables: list[str] | Unset = UNSET
        if not isinstance(self.unsupported_tables, Unset):
            unsupported_tables = self.unsupported_tables

        source_bytes = self.source_bytes

        target_free_bytes = self.target_free_bytes

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "outcome": outcome,
                "table_count": table_count,
                "sequence_count": sequence_count,
                "source_wal_limit_bytes": source_wal_limit_bytes,
                "free_source_slots": free_source_slots,
                "free_source_senders": free_source_senders,
            }
        )
        if reasons is not UNSET:
            field_dict["reasons"] = reasons
        if source_major is not UNSET:
            field_dict["source_major"] = source_major
        if target_major is not UNSET:
            field_dict["target_major"] = target_major
        if unsupported_table_count is not UNSET:
            field_dict["unsupported_table_count"] = unsupported_table_count
        if unsupported_tables is not UNSET:
            field_dict["unsupported_tables"] = unsupported_tables
        if source_bytes is not UNSET:
            field_dict["source_bytes"] = source_bytes
        if target_free_bytes is not UNSET:
            field_dict["target_free_bytes"] = target_free_bytes

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        outcome = check_managed_postgres_migration_check_outcome(d.pop("outcome"))

        table_count = d.pop("table_count")

        sequence_count = d.pop("sequence_count")

        source_wal_limit_bytes = d.pop("source_wal_limit_bytes")

        free_source_slots = d.pop("free_source_slots")

        free_source_senders = d.pop("free_source_senders")

        reasons = cast(list[str], d.pop("reasons", UNSET))

        source_major = d.pop("source_major", UNSET)

        target_major = d.pop("target_major", UNSET)

        unsupported_table_count = d.pop("unsupported_table_count", UNSET)

        unsupported_tables = cast(list[str], d.pop("unsupported_tables", UNSET))

        source_bytes = d.pop("source_bytes", UNSET)

        target_free_bytes = d.pop("target_free_bytes", UNSET)

        managed_postgres_migration_check = cls(
            outcome=outcome,
            table_count=table_count,
            sequence_count=sequence_count,
            source_wal_limit_bytes=source_wal_limit_bytes,
            free_source_slots=free_source_slots,
            free_source_senders=free_source_senders,
            reasons=reasons,
            source_major=source_major,
            target_major=target_major,
            unsupported_table_count=unsupported_table_count,
            unsupported_tables=unsupported_tables,
            source_bytes=source_bytes,
            target_free_bytes=target_free_bytes,
        )

        managed_postgres_migration_check.additional_properties = d
        return managed_postgres_migration_check

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
