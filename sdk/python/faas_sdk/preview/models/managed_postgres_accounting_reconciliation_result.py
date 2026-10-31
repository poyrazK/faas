from __future__ import annotations

import datetime
from collections.abc import Mapping
from typing import Any, TypeVar
from uuid import UUID

from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..types import UNSET, Unset

T = TypeVar("T", bound="ManagedPostgresAccountingReconciliationResult")


@_attrs_define
class ManagedPostgresAccountingReconciliationResult:
    """Preview or original committed repair response. Requires usage recovery describes the post-repair state; receipt
    replay is not a live admission or coverage read. Use accounting diagnostics for current status.

    """

    reconciliation_id: UUID
    """Identity of the previewed or committed repair receipt."""
    database_id: UUID
    """Reconciled catalog database."""
    revision: str
    """Revision fencing the reviewed request and relevant catalog/ledger/coverage state."""
    applied: bool
    """True for a committed repair or durable replay."""
    shutdown_at: datetime.datetime
    """Attested actual shutdown boundary."""
    observed_at: datetime.datetime
    """Preserved evidence observation time."""
    shared_accounting: bool
    """Restore descendant still shares its existing accounting root."""
    requires_usage_recovery: bool
    """Coverage resets on apply; recovery and final correction evidence must satisfy admission."""
    previous_deleted_at: datetime.datetime | Unset = UNSET
    """Previous logical deletion timestamp retained in the audit; it did not prove shutdown."""
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        reconciliation_id = str(self.reconciliation_id)

        database_id = str(self.database_id)

        revision = self.revision

        applied = self.applied

        shutdown_at = self.shutdown_at.isoformat()

        observed_at = self.observed_at.isoformat()

        shared_accounting = self.shared_accounting

        requires_usage_recovery = self.requires_usage_recovery

        previous_deleted_at: str | Unset = UNSET
        if not isinstance(self.previous_deleted_at, Unset):
            previous_deleted_at = self.previous_deleted_at.isoformat()

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "reconciliation_id": reconciliation_id,
                "database_id": database_id,
                "revision": revision,
                "applied": applied,
                "shutdown_at": shutdown_at,
                "observed_at": observed_at,
                "shared_accounting": shared_accounting,
                "requires_usage_recovery": requires_usage_recovery,
            }
        )
        if previous_deleted_at is not UNSET:
            field_dict["previous_deleted_at"] = previous_deleted_at

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        reconciliation_id = UUID(d.pop("reconciliation_id"))

        database_id = UUID(d.pop("database_id"))

        revision = d.pop("revision")

        applied = d.pop("applied")

        shutdown_at = datetime.datetime.fromisoformat(d.pop("shutdown_at"))

        observed_at = datetime.datetime.fromisoformat(d.pop("observed_at"))

        shared_accounting = d.pop("shared_accounting")

        requires_usage_recovery = d.pop("requires_usage_recovery")

        _previous_deleted_at = d.pop("previous_deleted_at", UNSET)
        previous_deleted_at: datetime.datetime | Unset
        if isinstance(_previous_deleted_at, Unset):
            previous_deleted_at = UNSET
        else:
            previous_deleted_at = datetime.datetime.fromisoformat(_previous_deleted_at)

        managed_postgres_accounting_reconciliation_result = cls(
            reconciliation_id=reconciliation_id,
            database_id=database_id,
            revision=revision,
            applied=applied,
            shutdown_at=shutdown_at,
            observed_at=observed_at,
            shared_accounting=shared_accounting,
            requires_usage_recovery=requires_usage_recovery,
            previous_deleted_at=previous_deleted_at,
        )

        managed_postgres_accounting_reconciliation_result.additional_properties = d
        return managed_postgres_accounting_reconciliation_result

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
