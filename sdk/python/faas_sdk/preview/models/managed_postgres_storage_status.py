from __future__ import annotations

from collections.abc import Mapping
from typing import TYPE_CHECKING, Any, TypeVar

from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..types import UNSET, Unset

if TYPE_CHECKING:
    from ..models.managed_postgres_auto_grow_status import ManagedPostgresAutoGrowStatus
    from ..models.managed_postgres_storage_alert import ManagedPostgresStorageAlert
    from ..models.managed_postgres_volume_status import ManagedPostgresVolumeStatus


T = TypeVar("T", bound="ManagedPostgresStorageStatus")


@_attrs_define
class ManagedPostgresStorageStatus:
    """Storage allocation and latest filesystem observations for a native PostgreSQL database."""

    allocated_bytes: int
    """Committed allocation per volume in bytes."""
    standby_expected: bool
    """Whether the database is configured to have a standby."""
    primary: ManagedPostgresVolumeStatus | Unset = UNSET
    """Last observed filesystem capacity and pressure for a native database volume."""
    standby: ManagedPostgresVolumeStatus | Unset = UNSET
    """Last observed filesystem capacity and pressure for a native database volume."""
    auto_grow: ManagedPostgresAutoGrowStatus | Unset = UNSET
    """Automatic growth policy and the latest storage eligibility check."""
    alerts: list[ManagedPostgresStorageAlert] | Unset = UNSET
    """Latest 10 recorded primary storage alert transitions, newest first."""
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        allocated_bytes = self.allocated_bytes

        standby_expected = self.standby_expected

        primary: dict[str, Any] | Unset = UNSET
        if not isinstance(self.primary, Unset):
            primary = self.primary.to_dict()

        standby: dict[str, Any] | Unset = UNSET
        if not isinstance(self.standby, Unset):
            standby = self.standby.to_dict()

        auto_grow: dict[str, Any] | Unset = UNSET
        if not isinstance(self.auto_grow, Unset):
            auto_grow = self.auto_grow.to_dict()

        alerts: list[dict[str, Any]] | Unset = UNSET
        if not isinstance(self.alerts, Unset):
            alerts = []
            for alerts_item_data in self.alerts:
                alerts_item = alerts_item_data.to_dict()
                alerts.append(alerts_item)

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "allocated_bytes": allocated_bytes,
                "standby_expected": standby_expected,
            }
        )
        if primary is not UNSET:
            field_dict["primary"] = primary
        if standby is not UNSET:
            field_dict["standby"] = standby
        if auto_grow is not UNSET:
            field_dict["auto_grow"] = auto_grow
        if alerts is not UNSET:
            field_dict["alerts"] = alerts

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        from ..models.managed_postgres_auto_grow_status import ManagedPostgresAutoGrowStatus
        from ..models.managed_postgres_storage_alert import ManagedPostgresStorageAlert
        from ..models.managed_postgres_volume_status import ManagedPostgresVolumeStatus

        d = dict(src_dict)
        allocated_bytes = d.pop("allocated_bytes")

        standby_expected = d.pop("standby_expected")

        _primary = d.pop("primary", UNSET)
        primary: ManagedPostgresVolumeStatus | Unset
        if isinstance(_primary, Unset):
            primary = UNSET
        else:
            primary = ManagedPostgresVolumeStatus.from_dict(_primary)

        _standby = d.pop("standby", UNSET)
        standby: ManagedPostgresVolumeStatus | Unset
        if isinstance(_standby, Unset):
            standby = UNSET
        else:
            standby = ManagedPostgresVolumeStatus.from_dict(_standby)

        _auto_grow = d.pop("auto_grow", UNSET)
        auto_grow: ManagedPostgresAutoGrowStatus | Unset
        if isinstance(_auto_grow, Unset):
            auto_grow = UNSET
        else:
            auto_grow = ManagedPostgresAutoGrowStatus.from_dict(_auto_grow)

        _alerts = d.pop("alerts", UNSET)
        alerts: list[ManagedPostgresStorageAlert] | Unset = UNSET
        if _alerts is not UNSET:
            alerts = []
            for alerts_item_data in _alerts:
                alerts_item = ManagedPostgresStorageAlert.from_dict(alerts_item_data)

                alerts.append(alerts_item)

        managed_postgres_storage_status = cls(
            allocated_bytes=allocated_bytes,
            standby_expected=standby_expected,
            primary=primary,
            standby=standby,
            auto_grow=auto_grow,
            alerts=alerts,
        )

        managed_postgres_storage_status.additional_properties = d
        return managed_postgres_storage_status

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
