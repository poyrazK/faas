from __future__ import annotations

import datetime
from collections.abc import Mapping
from typing import TYPE_CHECKING, Any, TypeVar

from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..types import UNSET, Unset

if TYPE_CHECKING:
    from ..models.managed_postgres_recovery_status import ManagedPostgresRecoveryStatus
    from ..models.managed_postgres_restore_drill_status import ManagedPostgresRestoreDrillStatus


T = TypeVar("T", bound="ManagedPostgresBackupStatus")


@_attrs_define
class ManagedPostgresBackupStatus:
    """Configured point-in-time restore retention and recorded backup and restore evidence for a native PostgreSQL
    database.

    """

    restore_window_seconds: int
    """Configured retention in seconds. Zero disables the restore window."""
    recovery: ManagedPostgresRecoveryStatus | Unset = UNSET
    """Latest archive evidence and recovery point for a configured destination region."""
    last_base_backup_at: datetime.datetime | Unset = UNSET
    """When the scheduler last recorded a successful base backup."""
    latest_restore_drill: ManagedPostgresRestoreDrillStatus | Unset = UNSET
    """Recorded evidence from a single restore drill for this database."""
    last_verified_restore: ManagedPostgresRestoreDrillStatus | Unset = UNSET
    """Recorded evidence from a single restore drill for this database."""
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        restore_window_seconds = self.restore_window_seconds

        recovery: dict[str, Any] | Unset = UNSET
        if not isinstance(self.recovery, Unset):
            recovery = self.recovery.to_dict()

        last_base_backup_at: str | Unset = UNSET
        if not isinstance(self.last_base_backup_at, Unset):
            last_base_backup_at = self.last_base_backup_at.isoformat()

        latest_restore_drill: dict[str, Any] | Unset = UNSET
        if not isinstance(self.latest_restore_drill, Unset):
            latest_restore_drill = self.latest_restore_drill.to_dict()

        last_verified_restore: dict[str, Any] | Unset = UNSET
        if not isinstance(self.last_verified_restore, Unset):
            last_verified_restore = self.last_verified_restore.to_dict()

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "restore_window_seconds": restore_window_seconds,
            }
        )
        if recovery is not UNSET:
            field_dict["recovery"] = recovery
        if last_base_backup_at is not UNSET:
            field_dict["last_base_backup_at"] = last_base_backup_at
        if latest_restore_drill is not UNSET:
            field_dict["latest_restore_drill"] = latest_restore_drill
        if last_verified_restore is not UNSET:
            field_dict["last_verified_restore"] = last_verified_restore

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        from ..models.managed_postgres_recovery_status import ManagedPostgresRecoveryStatus
        from ..models.managed_postgres_restore_drill_status import ManagedPostgresRestoreDrillStatus

        d = dict(src_dict)
        restore_window_seconds = d.pop("restore_window_seconds")

        _recovery = d.pop("recovery", UNSET)
        recovery: ManagedPostgresRecoveryStatus | Unset
        if isinstance(_recovery, Unset):
            recovery = UNSET
        else:
            recovery = ManagedPostgresRecoveryStatus.from_dict(_recovery)

        _last_base_backup_at = d.pop("last_base_backup_at", UNSET)
        last_base_backup_at: datetime.datetime | Unset
        if isinstance(_last_base_backup_at, Unset):
            last_base_backup_at = UNSET
        else:
            last_base_backup_at = datetime.datetime.fromisoformat(_last_base_backup_at)

        _latest_restore_drill = d.pop("latest_restore_drill", UNSET)
        latest_restore_drill: ManagedPostgresRestoreDrillStatus | Unset
        if isinstance(_latest_restore_drill, Unset):
            latest_restore_drill = UNSET
        else:
            latest_restore_drill = ManagedPostgresRestoreDrillStatus.from_dict(_latest_restore_drill)

        _last_verified_restore = d.pop("last_verified_restore", UNSET)
        last_verified_restore: ManagedPostgresRestoreDrillStatus | Unset
        if isinstance(_last_verified_restore, Unset):
            last_verified_restore = UNSET
        else:
            last_verified_restore = ManagedPostgresRestoreDrillStatus.from_dict(_last_verified_restore)

        managed_postgres_backup_status = cls(
            restore_window_seconds=restore_window_seconds,
            recovery=recovery,
            last_base_backup_at=last_base_backup_at,
            latest_restore_drill=latest_restore_drill,
            last_verified_restore=last_verified_restore,
        )

        managed_postgres_backup_status.additional_properties = d
        return managed_postgres_backup_status

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
