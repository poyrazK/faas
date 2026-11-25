from __future__ import annotations

import datetime
from collections.abc import Mapping
from typing import Any, TypeVar
from uuid import UUID

from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..models.managed_postgres_restore_drill_status_outcome import (
    ManagedPostgresRestoreDrillStatusOutcome,
    check_managed_postgres_restore_drill_status_outcome,
)
from ..types import UNSET, Unset

T = TypeVar("T", bound="ManagedPostgresRestoreDrillStatus")


@_attrs_define
class ManagedPostgresRestoreDrillStatus:
    """Recorded evidence from a single restore drill for this database."""

    outcome: ManagedPostgresRestoreDrillStatusOutcome
    started_at: datetime.datetime
    drill_id: UUID | Unset = UNSET
    """ID of a requested drill."""
    target_at: datetime.datetime | Unset = UNSET
    """Recovery point after the source commits the marker."""
    finished_at: datetime.datetime | Unset = UNSET
    recovered_to: datetime.datetime | Unset = UNSET
    """The last replayed commit reported by PostgreSQL."""
    error_code: str | Unset = UNSET
    """Stable reason when a drill could not verify recovery."""
    duration_seconds: int | Unset = UNSET
    """Total time from admission through cleanup."""
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        outcome: str = self.outcome

        started_at = self.started_at.isoformat()

        drill_id: str | Unset = UNSET
        if not isinstance(self.drill_id, Unset):
            drill_id = str(self.drill_id)

        target_at: str | Unset = UNSET
        if not isinstance(self.target_at, Unset):
            target_at = self.target_at.isoformat()

        finished_at: str | Unset = UNSET
        if not isinstance(self.finished_at, Unset):
            finished_at = self.finished_at.isoformat()

        recovered_to: str | Unset = UNSET
        if not isinstance(self.recovered_to, Unset):
            recovered_to = self.recovered_to.isoformat()

        error_code = self.error_code

        duration_seconds = self.duration_seconds

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "outcome": outcome,
                "started_at": started_at,
            }
        )
        if drill_id is not UNSET:
            field_dict["drill_id"] = drill_id
        if target_at is not UNSET:
            field_dict["target_at"] = target_at
        if finished_at is not UNSET:
            field_dict["finished_at"] = finished_at
        if recovered_to is not UNSET:
            field_dict["recovered_to"] = recovered_to
        if error_code is not UNSET:
            field_dict["error_code"] = error_code
        if duration_seconds is not UNSET:
            field_dict["duration_seconds"] = duration_seconds

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        outcome = check_managed_postgres_restore_drill_status_outcome(d.pop("outcome"))

        started_at = datetime.datetime.fromisoformat(d.pop("started_at"))

        _drill_id = d.pop("drill_id", UNSET)
        drill_id: UUID | Unset
        if isinstance(_drill_id, Unset):
            drill_id = UNSET
        else:
            drill_id = UUID(_drill_id)

        _target_at = d.pop("target_at", UNSET)
        target_at: datetime.datetime | Unset
        if isinstance(_target_at, Unset):
            target_at = UNSET
        else:
            target_at = datetime.datetime.fromisoformat(_target_at)

        _finished_at = d.pop("finished_at", UNSET)
        finished_at: datetime.datetime | Unset
        if isinstance(_finished_at, Unset):
            finished_at = UNSET
        else:
            finished_at = datetime.datetime.fromisoformat(_finished_at)

        _recovered_to = d.pop("recovered_to", UNSET)
        recovered_to: datetime.datetime | Unset
        if isinstance(_recovered_to, Unset):
            recovered_to = UNSET
        else:
            recovered_to = datetime.datetime.fromisoformat(_recovered_to)

        error_code = d.pop("error_code", UNSET)

        duration_seconds = d.pop("duration_seconds", UNSET)

        managed_postgres_restore_drill_status = cls(
            outcome=outcome,
            started_at=started_at,
            drill_id=drill_id,
            target_at=target_at,
            finished_at=finished_at,
            recovered_to=recovered_to,
            error_code=error_code,
            duration_seconds=duration_seconds,
        )

        managed_postgres_restore_drill_status.additional_properties = d
        return managed_postgres_restore_drill_status

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
