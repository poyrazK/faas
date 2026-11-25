from __future__ import annotations

import datetime
from collections.abc import Mapping
from typing import TYPE_CHECKING, Any, TypeVar

from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..models.resource_status_kind import ResourceStatusKind, check_resource_status_kind
from ..types import UNSET, Unset

if TYPE_CHECKING:
    from ..models.condition import Condition
    from ..models.managed_postgres_backup_status import ManagedPostgresBackupStatus
    from ..models.managed_postgres_maintenance_status import ManagedPostgresMaintenanceStatus
    from ..models.managed_postgres_replica_status import ManagedPostgresReplicaStatus
    from ..models.managed_postgres_storage_status import ManagedPostgresStorageStatus


T = TypeVar("T", bound="ResourceStatus")


@_attrs_define
class ResourceStatus:
    """A resource's status subresource: conditions derived when read, never stored."""

    kind: ResourceStatusKind
    name: str
    """The app slug, deployment ID, database name, domain, cron ID or job run ID."""
    observed_at: datetime.datetime
    """When the evidence behind the conditions was read."""
    conditions: list[Condition]
    storage: ManagedPostgresStorageStatus | Unset = UNSET
    """Storage allocation and latest filesystem observations for a native PostgreSQL database."""
    maintenance: ManagedPostgresMaintenanceStatus | Unset = UNSET
    backup: ManagedPostgresBackupStatus | Unset = UNSET
    """Configured point-in-time restore retention and recorded backup and restore evidence for a native PostgreSQL
    database."""
    replica: ManagedPostgresReplicaStatus | Unset = UNSET
    """Standby health sampled without waking a native production database."""
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        kind: str = self.kind

        name = self.name

        observed_at = self.observed_at.isoformat()

        conditions = []
        for conditions_item_data in self.conditions:
            conditions_item = conditions_item_data.to_dict()
            conditions.append(conditions_item)

        storage: dict[str, Any] | Unset = UNSET
        if not isinstance(self.storage, Unset):
            storage = self.storage.to_dict()

        maintenance: dict[str, Any] | Unset = UNSET
        if not isinstance(self.maintenance, Unset):
            maintenance = self.maintenance.to_dict()

        backup: dict[str, Any] | Unset = UNSET
        if not isinstance(self.backup, Unset):
            backup = self.backup.to_dict()

        replica: dict[str, Any] | Unset = UNSET
        if not isinstance(self.replica, Unset):
            replica = self.replica.to_dict()

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "kind": kind,
                "name": name,
                "observed_at": observed_at,
                "conditions": conditions,
            }
        )
        if storage is not UNSET:
            field_dict["storage"] = storage
        if maintenance is not UNSET:
            field_dict["maintenance"] = maintenance
        if backup is not UNSET:
            field_dict["backup"] = backup
        if replica is not UNSET:
            field_dict["replica"] = replica

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        from ..models.condition import Condition
        from ..models.managed_postgres_backup_status import ManagedPostgresBackupStatus
        from ..models.managed_postgres_maintenance_status import ManagedPostgresMaintenanceStatus
        from ..models.managed_postgres_replica_status import ManagedPostgresReplicaStatus
        from ..models.managed_postgres_storage_status import ManagedPostgresStorageStatus

        d = dict(src_dict)
        kind = check_resource_status_kind(d.pop("kind"))

        name = d.pop("name")

        observed_at = datetime.datetime.fromisoformat(d.pop("observed_at"))

        conditions = []
        _conditions = d.pop("conditions")
        for conditions_item_data in _conditions:
            conditions_item = Condition.from_dict(conditions_item_data)

            conditions.append(conditions_item)

        _storage = d.pop("storage", UNSET)
        storage: ManagedPostgresStorageStatus | Unset
        if isinstance(_storage, Unset):
            storage = UNSET
        else:
            storage = ManagedPostgresStorageStatus.from_dict(_storage)

        _maintenance = d.pop("maintenance", UNSET)
        maintenance: ManagedPostgresMaintenanceStatus | Unset
        if isinstance(_maintenance, Unset):
            maintenance = UNSET
        else:
            maintenance = ManagedPostgresMaintenanceStatus.from_dict(_maintenance)

        _backup = d.pop("backup", UNSET)
        backup: ManagedPostgresBackupStatus | Unset
        if isinstance(_backup, Unset):
            backup = UNSET
        else:
            backup = ManagedPostgresBackupStatus.from_dict(_backup)

        _replica = d.pop("replica", UNSET)
        replica: ManagedPostgresReplicaStatus | Unset
        if isinstance(_replica, Unset):
            replica = UNSET
        else:
            replica = ManagedPostgresReplicaStatus.from_dict(_replica)

        resource_status = cls(
            kind=kind,
            name=name,
            observed_at=observed_at,
            conditions=conditions,
            storage=storage,
            maintenance=maintenance,
            backup=backup,
            replica=replica,
        )

        resource_status.additional_properties = d
        return resource_status

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
