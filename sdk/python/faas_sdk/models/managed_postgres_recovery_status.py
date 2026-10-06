from __future__ import annotations

import datetime
from collections.abc import Mapping
from typing import Any, TypeVar

from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..models.managed_postgres_recovery_status_status import (
    ManagedPostgresRecoveryStatusStatus,
    check_managed_postgres_recovery_status_status,
)
from ..types import UNSET, Unset

T = TypeVar("T", bound="ManagedPostgresRecoveryStatus")


@_attrs_define
class ManagedPostgresRecoveryStatus:
    """Live metadata recovery observation. limits_known establishes necessary limits only; available additionally requires
    authoritative provider history bounds. Neither is an atomic guarantee of a later restore. Private provider
    identifiers and credentials are excluded.

    """

    database_id: str
    status: ManagedPostgresRecoveryStatusStatus
    fresh: bool
    """True only when current source metadata was successfully validated."""
    history_bounds_known: bool
    """True only when the provider supplies authoritative retained history bounds. Neon currently supplies
    necessary limits only."""
    retention_seconds: int
    """Effective observed retention intersected with catalog limits; zero when unconfirmed or disabled."""
    checked_at: datetime.datetime | Unset = UNSET
    """Completion time of the latest provider observation attempt."""
    earliest_possible_time: datetime.datetime | Unset = UNSET
    """Necessary lower limit; does not by itself prove retained WAL begins here."""
    latest_possible_time: datetime.datetime | Unset = UNSET
    """Necessary upper limit; new restore points must also precede checked_at."""
    last_error_code: str | Unset = UNSET
    """Stable non-sensitive diagnostic; no provider response text."""
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        database_id = self.database_id

        status: str = self.status

        fresh = self.fresh

        history_bounds_known = self.history_bounds_known

        retention_seconds = self.retention_seconds

        checked_at: str | Unset = UNSET
        if not isinstance(self.checked_at, Unset):
            checked_at = self.checked_at.isoformat()

        earliest_possible_time: str | Unset = UNSET
        if not isinstance(self.earliest_possible_time, Unset):
            earliest_possible_time = self.earliest_possible_time.isoformat()

        latest_possible_time: str | Unset = UNSET
        if not isinstance(self.latest_possible_time, Unset):
            latest_possible_time = self.latest_possible_time.isoformat()

        last_error_code = self.last_error_code

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "database_id": database_id,
                "status": status,
                "fresh": fresh,
                "history_bounds_known": history_bounds_known,
                "retention_seconds": retention_seconds,
            }
        )
        if checked_at is not UNSET:
            field_dict["checked_at"] = checked_at
        if earliest_possible_time is not UNSET:
            field_dict["earliest_possible_time"] = earliest_possible_time
        if latest_possible_time is not UNSET:
            field_dict["latest_possible_time"] = latest_possible_time
        if last_error_code is not UNSET:
            field_dict["last_error_code"] = last_error_code

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        database_id = d.pop("database_id")

        status = check_managed_postgres_recovery_status_status(d.pop("status"))

        fresh = d.pop("fresh")

        history_bounds_known = d.pop("history_bounds_known")

        retention_seconds = d.pop("retention_seconds")

        _checked_at = d.pop("checked_at", UNSET)
        checked_at: datetime.datetime | Unset
        if isinstance(_checked_at, Unset):
            checked_at = UNSET
        else:
            checked_at = datetime.datetime.fromisoformat(_checked_at)

        _earliest_possible_time = d.pop("earliest_possible_time", UNSET)
        earliest_possible_time: datetime.datetime | Unset
        if isinstance(_earliest_possible_time, Unset):
            earliest_possible_time = UNSET
        else:
            earliest_possible_time = datetime.datetime.fromisoformat(_earliest_possible_time)

        _latest_possible_time = d.pop("latest_possible_time", UNSET)
        latest_possible_time: datetime.datetime | Unset
        if isinstance(_latest_possible_time, Unset):
            latest_possible_time = UNSET
        else:
            latest_possible_time = datetime.datetime.fromisoformat(_latest_possible_time)

        last_error_code = d.pop("last_error_code", UNSET)

        managed_postgres_recovery_status = cls(
            database_id=database_id,
            status=status,
            fresh=fresh,
            history_bounds_known=history_bounds_known,
            retention_seconds=retention_seconds,
            checked_at=checked_at,
            earliest_possible_time=earliest_possible_time,
            latest_possible_time=latest_possible_time,
            last_error_code=last_error_code,
        )

        managed_postgres_recovery_status.additional_properties = d
        return managed_postgres_recovery_status

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
