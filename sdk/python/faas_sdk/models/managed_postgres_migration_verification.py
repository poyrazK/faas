from __future__ import annotations

import datetime
from collections.abc import Mapping
from typing import Any, TypeVar
from uuid import UUID

from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..models.managed_postgres_migration_verification_state import (
    ManagedPostgresMigrationVerificationState,
    check_managed_postgres_migration_verification_state,
)
from ..types import UNSET, Unset

T = TypeVar("T", bound="ManagedPostgresMigrationVerification")


@_attrs_define
class ManagedPostgresMigrationVerification:
    """Durable replay evidence for one operator-declared source write pause."""

    verification_id: UUID
    state: ManagedPostgresMigrationVerificationState
    sequences_synced: int
    reason: str | Unset = UNSET
    applied_lsn: str | Unset = UNSET
    """Flushed subscriber origin position sampled after seeing the marker."""
    verified_at: datetime.datetime | Unset = UNSET
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        verification_id = str(self.verification_id)

        state: str = self.state

        sequences_synced = self.sequences_synced

        reason = self.reason

        applied_lsn = self.applied_lsn

        verified_at: str | Unset = UNSET
        if not isinstance(self.verified_at, Unset):
            verified_at = self.verified_at.isoformat()

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "verification_id": verification_id,
                "state": state,
                "sequences_synced": sequences_synced,
            }
        )
        if reason is not UNSET:
            field_dict["reason"] = reason
        if applied_lsn is not UNSET:
            field_dict["applied_lsn"] = applied_lsn
        if verified_at is not UNSET:
            field_dict["verified_at"] = verified_at

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        verification_id = UUID(d.pop("verification_id"))

        state = check_managed_postgres_migration_verification_state(d.pop("state"))

        sequences_synced = d.pop("sequences_synced")

        reason = d.pop("reason", UNSET)

        applied_lsn = d.pop("applied_lsn", UNSET)

        _verified_at = d.pop("verified_at", UNSET)
        verified_at: datetime.datetime | Unset
        if isinstance(_verified_at, Unset):
            verified_at = UNSET
        else:
            verified_at = datetime.datetime.fromisoformat(_verified_at)

        managed_postgres_migration_verification = cls(
            verification_id=verification_id,
            state=state,
            sequences_synced=sequences_synced,
            reason=reason,
            applied_lsn=applied_lsn,
            verified_at=verified_at,
        )

        managed_postgres_migration_verification.additional_properties = d
        return managed_postgres_migration_verification

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
