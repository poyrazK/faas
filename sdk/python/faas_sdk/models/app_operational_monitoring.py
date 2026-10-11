from __future__ import annotations

import datetime
from collections.abc import Mapping
from typing import TYPE_CHECKING, Any, TypeVar

from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..types import UNSET, Unset

if TYPE_CHECKING:
    from ..models.app_operational_incident import AppOperationalIncident


T = TypeVar("T", bound="AppOperationalMonitoring")


@_attrs_define
class AppOperationalMonitoring:
    """Recent default-scope route-monitor evaluation with observed-only coverage. Unknown or unavailable health is
    independent of deployment smoke verification.

    """

    available: bool
    status: str
    reason: str
    coverage: str
    incidents_available: bool
    deployment_id: str | Unset = UNSET
    checked_at: datetime.datetime | Unset = UNSET
    window_start: datetime.datetime | Unset = UNSET
    window_end: datetime.datetime | Unset = UNSET
    incident: AppOperationalIncident | Unset = UNSET
    """Metadata for the app's open production-route incident. Reading this summary never closes the incident or
    includes saved request evidence."""
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        available = self.available

        status = self.status

        reason = self.reason

        coverage = self.coverage

        incidents_available = self.incidents_available

        deployment_id = self.deployment_id

        checked_at: str | Unset = UNSET
        if not isinstance(self.checked_at, Unset):
            checked_at = self.checked_at.isoformat()

        window_start: str | Unset = UNSET
        if not isinstance(self.window_start, Unset):
            window_start = self.window_start.isoformat()

        window_end: str | Unset = UNSET
        if not isinstance(self.window_end, Unset):
            window_end = self.window_end.isoformat()

        incident: dict[str, Any] | Unset = UNSET
        if not isinstance(self.incident, Unset):
            incident = self.incident.to_dict()

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "available": available,
                "status": status,
                "reason": reason,
                "coverage": coverage,
                "incidents_available": incidents_available,
            }
        )
        if deployment_id is not UNSET:
            field_dict["deployment_id"] = deployment_id
        if checked_at is not UNSET:
            field_dict["checked_at"] = checked_at
        if window_start is not UNSET:
            field_dict["window_start"] = window_start
        if window_end is not UNSET:
            field_dict["window_end"] = window_end
        if incident is not UNSET:
            field_dict["incident"] = incident

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        from ..models.app_operational_incident import AppOperationalIncident

        d = dict(src_dict)
        available = d.pop("available")

        status = d.pop("status")

        reason = d.pop("reason")

        coverage = d.pop("coverage")

        incidents_available = d.pop("incidents_available")

        deployment_id = d.pop("deployment_id", UNSET)

        _checked_at = d.pop("checked_at", UNSET)
        checked_at: datetime.datetime | Unset
        if isinstance(_checked_at, Unset):
            checked_at = UNSET
        else:
            checked_at = datetime.datetime.fromisoformat(_checked_at)

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

        _incident = d.pop("incident", UNSET)
        incident: AppOperationalIncident | Unset
        if isinstance(_incident, Unset):
            incident = UNSET
        else:
            incident = AppOperationalIncident.from_dict(_incident)

        app_operational_monitoring = cls(
            available=available,
            status=status,
            reason=reason,
            coverage=coverage,
            incidents_available=incidents_available,
            deployment_id=deployment_id,
            checked_at=checked_at,
            window_start=window_start,
            window_end=window_end,
            incident=incident,
        )

        app_operational_monitoring.additional_properties = d
        return app_operational_monitoring

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
