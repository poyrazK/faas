from __future__ import annotations

from collections.abc import Mapping
from typing import TYPE_CHECKING, Any, TypeVar, cast

from attrs import define as _attrs_define
from attrs import field as _attrs_field

if TYPE_CHECKING:
    from ..models.managed_postgres_maintenance_window import ManagedPostgresMaintenanceWindow


T = TypeVar("T", bound="SetManagedPostgresMaintenanceRequest")


@_attrs_define
class SetManagedPostgresMaintenanceRequest:
    window: ManagedPostgresMaintenanceWindow | None
    """Use null to remove the schedule."""
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        from ..models.managed_postgres_maintenance_window import ManagedPostgresMaintenanceWindow

        window: dict[str, Any] | None
        if isinstance(self.window, ManagedPostgresMaintenanceWindow):
            window = self.window.to_dict()
        else:
            window = self.window

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "window": window,
            }
        )

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
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

        set_managed_postgres_maintenance_request = cls(
            window=window,
        )

        set_managed_postgres_maintenance_request.additional_properties = d
        return set_managed_postgres_maintenance_request

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
