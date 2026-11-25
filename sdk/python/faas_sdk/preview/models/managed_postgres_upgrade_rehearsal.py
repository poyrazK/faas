from __future__ import annotations

import datetime
from collections.abc import Mapping
from typing import Any, TypeVar
from uuid import UUID

from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..models.managed_postgres_upgrade_rehearsal_failed_phase import (
    ManagedPostgresUpgradeRehearsalFailedPhase,
    check_managed_postgres_upgrade_rehearsal_failed_phase,
)
from ..models.managed_postgres_upgrade_rehearsal_outcome import (
    ManagedPostgresUpgradeRehearsalOutcome,
    check_managed_postgres_upgrade_rehearsal_outcome,
)
from ..models.managed_postgres_upgrade_rehearsal_phase import (
    ManagedPostgresUpgradeRehearsalPhase,
    check_managed_postgres_upgrade_rehearsal_phase,
)
from ..types import UNSET, Unset

T = TypeVar("T", bound="ManagedPostgresUpgradeRehearsal")


@_attrs_define
class ManagedPostgresUpgradeRehearsal:
    """Evidence from a PostgreSQL major upgrade run on a temporary copy."""

    database_id: UUID
    rehearsal_id: UUID
    request_id: UUID
    source_major: int
    target_major: int
    outcome: ManagedPostgresUpgradeRehearsalOutcome
    started_at: datetime.datetime
    phase: ManagedPostgresUpgradeRehearsalPhase | Unset = UNSET
    """The active phase."""
    failed_phase: ManagedPostgresUpgradeRehearsalFailedPhase | Unset = UNSET
    """The phase that failed."""
    recovery_point: datetime.datetime | Unset = UNSET
    """The archive-verified recovery target."""
    recovered_to: datetime.datetime | Unset = UNSET
    """The last replayed commit reported by PostgreSQL."""
    finished_at: datetime.datetime | Unset = UNSET
    duration_seconds: int | Unset = UNSET
    """Total time from admission through cleanup."""
    error_code: str | Unset = UNSET
    """A stable reason for a failed rehearsal."""
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        database_id = str(self.database_id)

        rehearsal_id = str(self.rehearsal_id)

        request_id = str(self.request_id)

        source_major = self.source_major

        target_major = self.target_major

        outcome: str = self.outcome

        started_at = self.started_at.isoformat()

        phase: str | Unset = UNSET
        if not isinstance(self.phase, Unset):
            phase = self.phase

        failed_phase: str | Unset = UNSET
        if not isinstance(self.failed_phase, Unset):
            failed_phase = self.failed_phase

        recovery_point: str | Unset = UNSET
        if not isinstance(self.recovery_point, Unset):
            recovery_point = self.recovery_point.isoformat()

        recovered_to: str | Unset = UNSET
        if not isinstance(self.recovered_to, Unset):
            recovered_to = self.recovered_to.isoformat()

        finished_at: str | Unset = UNSET
        if not isinstance(self.finished_at, Unset):
            finished_at = self.finished_at.isoformat()

        duration_seconds = self.duration_seconds

        error_code = self.error_code

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "database_id": database_id,
                "rehearsal_id": rehearsal_id,
                "request_id": request_id,
                "source_major": source_major,
                "target_major": target_major,
                "outcome": outcome,
                "started_at": started_at,
            }
        )
        if phase is not UNSET:
            field_dict["phase"] = phase
        if failed_phase is not UNSET:
            field_dict["failed_phase"] = failed_phase
        if recovery_point is not UNSET:
            field_dict["recovery_point"] = recovery_point
        if recovered_to is not UNSET:
            field_dict["recovered_to"] = recovered_to
        if finished_at is not UNSET:
            field_dict["finished_at"] = finished_at
        if duration_seconds is not UNSET:
            field_dict["duration_seconds"] = duration_seconds
        if error_code is not UNSET:
            field_dict["error_code"] = error_code

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        database_id = UUID(d.pop("database_id"))

        rehearsal_id = UUID(d.pop("rehearsal_id"))

        request_id = UUID(d.pop("request_id"))

        source_major = d.pop("source_major")

        target_major = d.pop("target_major")

        outcome = check_managed_postgres_upgrade_rehearsal_outcome(d.pop("outcome"))

        started_at = datetime.datetime.fromisoformat(d.pop("started_at"))

        _phase = d.pop("phase", UNSET)
        phase: ManagedPostgresUpgradeRehearsalPhase | Unset
        if isinstance(_phase, Unset):
            phase = UNSET
        else:
            phase = check_managed_postgres_upgrade_rehearsal_phase(_phase)

        _failed_phase = d.pop("failed_phase", UNSET)
        failed_phase: ManagedPostgresUpgradeRehearsalFailedPhase | Unset
        if isinstance(_failed_phase, Unset):
            failed_phase = UNSET
        else:
            failed_phase = check_managed_postgres_upgrade_rehearsal_failed_phase(_failed_phase)

        _recovery_point = d.pop("recovery_point", UNSET)
        recovery_point: datetime.datetime | Unset
        if isinstance(_recovery_point, Unset):
            recovery_point = UNSET
        else:
            recovery_point = datetime.datetime.fromisoformat(_recovery_point)

        _recovered_to = d.pop("recovered_to", UNSET)
        recovered_to: datetime.datetime | Unset
        if isinstance(_recovered_to, Unset):
            recovered_to = UNSET
        else:
            recovered_to = datetime.datetime.fromisoformat(_recovered_to)

        _finished_at = d.pop("finished_at", UNSET)
        finished_at: datetime.datetime | Unset
        if isinstance(_finished_at, Unset):
            finished_at = UNSET
        else:
            finished_at = datetime.datetime.fromisoformat(_finished_at)

        duration_seconds = d.pop("duration_seconds", UNSET)

        error_code = d.pop("error_code", UNSET)

        managed_postgres_upgrade_rehearsal = cls(
            database_id=database_id,
            rehearsal_id=rehearsal_id,
            request_id=request_id,
            source_major=source_major,
            target_major=target_major,
            outcome=outcome,
            started_at=started_at,
            phase=phase,
            failed_phase=failed_phase,
            recovery_point=recovery_point,
            recovered_to=recovered_to,
            finished_at=finished_at,
            duration_seconds=duration_seconds,
            error_code=error_code,
        )

        managed_postgres_upgrade_rehearsal.additional_properties = d
        return managed_postgres_upgrade_rehearsal

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
