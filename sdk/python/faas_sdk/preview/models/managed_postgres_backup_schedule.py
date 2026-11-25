from __future__ import annotations

import datetime
from collections.abc import Mapping
from typing import Any, TypeVar
from uuid import UUID

from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..models.managed_postgres_backup_schedule_state import (
    ManagedPostgresBackupScheduleState,
    check_managed_postgres_backup_schedule_state,
)
from ..types import UNSET, Unset

T = TypeVar("T", bound="ManagedPostgresBackupSchedule")


@_attrs_define
class ManagedPostgresBackupSchedule:
    """Daily export schedule, destination, and recorded delivery outcome."""

    database_id: UUID
    bucket_app_id: UUID
    bucket_id: UUID
    daily_at_utc: str
    next_run_at: datetime.datetime
    state: ManagedPostgresBackupScheduleState
    active_run_id: UUID | Unset = UNSET
    last_success_at: datetime.datetime | Unset = UNSET
    last_archive_key: str | Unset = UNSET
    """Object key of the most recent complete backup."""
    last_error_code: str | Unset = UNSET
    """Stable code for the latest backup attempt failure."""
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        database_id = str(self.database_id)

        bucket_app_id = str(self.bucket_app_id)

        bucket_id = str(self.bucket_id)

        daily_at_utc = self.daily_at_utc

        next_run_at = self.next_run_at.isoformat()

        state: str = self.state

        active_run_id: str | Unset = UNSET
        if not isinstance(self.active_run_id, Unset):
            active_run_id = str(self.active_run_id)

        last_success_at: str | Unset = UNSET
        if not isinstance(self.last_success_at, Unset):
            last_success_at = self.last_success_at.isoformat()

        last_archive_key = self.last_archive_key

        last_error_code = self.last_error_code

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "database_id": database_id,
                "bucket_app_id": bucket_app_id,
                "bucket_id": bucket_id,
                "daily_at_utc": daily_at_utc,
                "next_run_at": next_run_at,
                "state": state,
            }
        )
        if active_run_id is not UNSET:
            field_dict["active_run_id"] = active_run_id
        if last_success_at is not UNSET:
            field_dict["last_success_at"] = last_success_at
        if last_archive_key is not UNSET:
            field_dict["last_archive_key"] = last_archive_key
        if last_error_code is not UNSET:
            field_dict["last_error_code"] = last_error_code

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        database_id = UUID(d.pop("database_id"))

        bucket_app_id = UUID(d.pop("bucket_app_id"))

        bucket_id = UUID(d.pop("bucket_id"))

        daily_at_utc = d.pop("daily_at_utc")

        next_run_at = datetime.datetime.fromisoformat(d.pop("next_run_at"))

        state = check_managed_postgres_backup_schedule_state(d.pop("state"))

        _active_run_id = d.pop("active_run_id", UNSET)
        active_run_id: UUID | Unset
        if isinstance(_active_run_id, Unset):
            active_run_id = UNSET
        else:
            active_run_id = UUID(_active_run_id)

        _last_success_at = d.pop("last_success_at", UNSET)
        last_success_at: datetime.datetime | Unset
        if isinstance(_last_success_at, Unset):
            last_success_at = UNSET
        else:
            last_success_at = datetime.datetime.fromisoformat(_last_success_at)

        last_archive_key = d.pop("last_archive_key", UNSET)

        last_error_code = d.pop("last_error_code", UNSET)

        managed_postgres_backup_schedule = cls(
            database_id=database_id,
            bucket_app_id=bucket_app_id,
            bucket_id=bucket_id,
            daily_at_utc=daily_at_utc,
            next_run_at=next_run_at,
            state=state,
            active_run_id=active_run_id,
            last_success_at=last_success_at,
            last_archive_key=last_archive_key,
            last_error_code=last_error_code,
        )

        managed_postgres_backup_schedule.additional_properties = d
        return managed_postgres_backup_schedule

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
