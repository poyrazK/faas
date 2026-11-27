from __future__ import annotations

import datetime
from collections.abc import Mapping
from typing import TYPE_CHECKING, Any, TypeVar, cast

from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..types import UNSET, Unset

if TYPE_CHECKING:
    from ..models.managed_postgres_base_refresh import ManagedPostgresBaseRefresh
    from ..models.managed_postgres_maintenance_window import ManagedPostgresMaintenanceWindow


T = TypeVar("T", bound="ManagedPostgresMaintenanceStatus")


@_attrs_define
class ManagedPostgresMaintenanceStatus:
    """Configured maintenance window and the current database refresh status."""

    window: ManagedPostgresMaintenanceWindow | None
    window_open: bool
    window_start: datetime.datetime | Unset = UNSET
    """Start of the current or next window."""
    window_end: datetime.datetime | Unset = UNSET
    """Exclusive end of the current or next window."""
    stale_since: datetime.datetime | Unset = UNSET
    """First stale observation for the pending base refresh."""
    last_refresh: ManagedPostgresBaseRefresh | Unset = UNSET
    """Progress and outcome of replacing the database VM base image."""
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        from ..models.managed_postgres_maintenance_window import ManagedPostgresMaintenanceWindow

        window: dict[str, Any] | None
        if isinstance(self.window, ManagedPostgresMaintenanceWindow):
            window = self.window.to_dict()
        else:
            window = self.window

        window_open = self.window_open

        window_start: str | Unset = UNSET
        if not isinstance(self.window_start, Unset):
            window_start = self.window_start.isoformat()

        window_end: str | Unset = UNSET
        if not isinstance(self.window_end, Unset):
            window_end = self.window_end.isoformat()

        stale_since: str | Unset = UNSET
        if not isinstance(self.stale_since, Unset):
            stale_since = self.stale_since.isoformat()

        last_refresh: dict[str, Any] | Unset = UNSET
        if not isinstance(self.last_refresh, Unset):
            last_refresh = self.last_refresh.to_dict()

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "window": window,
                "window_open": window_open,
            }
        )
        if window_start is not UNSET:
            field_dict["window_start"] = window_start
        if window_end is not UNSET:
            field_dict["window_end"] = window_end
        if stale_since is not UNSET:
            field_dict["stale_since"] = stale_since
        if last_refresh is not UNSET:
            field_dict["last_refresh"] = last_refresh

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        from ..models.managed_postgres_base_refresh import ManagedPostgresBaseRefresh
        from ..models.managed_postgres_maintenance_window import ManagedPostgresMaintenanceWindow

        d = dict(src_dict)

        def _parse_window(data: object) -> ManagedPostgresMaintenanceWindow | None:
            if data is None:
                return data
            try:
                if not isinstance(data, dict):
                    raise TypeError()
                window_type_0 = ManagedPostgresMaintenanceWindow.from_dict(data)

                return window_type_0
            except (TypeError, ValueError, AttributeError, KeyError):
                pass
            return cast(ManagedPostgresMaintenanceWindow | None, data)

        window = _parse_window(d.pop("window"))

        window_open = d.pop("window_open")

        _window_start = d.pop("window_start", UNSET)
        window_start: datetime.datetime | Unset
        if isinstance(_window_start, Unset):
            window_start = UNSET
        else:
            window_start = datetime.datetime.fromisoformat(_window_start)

        _window_end = d.pop("window_end", UNSET)
        window_end: datetime.datetime | Unset
        if isinstance(_window_end, Unset):
            window_end = UNSET
        else:
            window_end = datetime.datetime.fromisoformat(_window_end)

        _stale_since = d.pop("stale_since", UNSET)
        stale_since: datetime.datetime | Unset
        if isinstance(_stale_since, Unset):
            stale_since = UNSET
        else:
            stale_since = datetime.datetime.fromisoformat(_stale_since)

        _last_refresh = d.pop("last_refresh", UNSET)
        last_refresh: ManagedPostgresBaseRefresh | Unset
        if isinstance(_last_refresh, Unset):
            last_refresh = UNSET
        else:
            last_refresh = ManagedPostgresBaseRefresh.from_dict(_last_refresh)

        managed_postgres_maintenance_status = cls(
            window=window,
            window_open=window_open,
            window_start=window_start,
            window_end=window_end,
            stale_since=stale_since,
            last_refresh=last_refresh,
        )

        managed_postgres_maintenance_status.additional_properties = d
        return managed_postgres_maintenance_status

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
