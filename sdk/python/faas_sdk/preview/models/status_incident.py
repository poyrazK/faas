from __future__ import annotations

import datetime
from collections.abc import Mapping
from typing import Any, TypeVar, cast

from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..models.status_incident_severity import StatusIncidentSeverity, check_status_incident_severity
from ..types import UNSET, Unset

T = TypeVar("T", bound="StatusIncident")


@_attrs_define
class StatusIncident:
    """Public operator-authored status incident."""

    started_at: datetime.datetime
    resolved_at: datetime.datetime | None
    severity: StatusIncidentSeverity
    summary: str
    component: str | Unset = UNSET
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        started_at = self.started_at.isoformat()

        resolved_at: None | str
        if isinstance(self.resolved_at, datetime.datetime):
            resolved_at = self.resolved_at.isoformat()
        else:
            resolved_at = self.resolved_at

        severity: str = self.severity

        summary = self.summary

        component = self.component

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "started_at": started_at,
                "resolved_at": resolved_at,
                "severity": severity,
                "summary": summary,
            }
        )
        if component is not UNSET:
            field_dict["component"] = component

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        started_at = datetime.datetime.fromisoformat(d.pop("started_at"))

        def _parse_resolved_at(data: object) -> datetime.datetime | None:
            if data is None:
                return data
            try:
                if not isinstance(data, str):
                    raise TypeError()
                resolved_at_type_0 = datetime.datetime.fromisoformat(data)

                return resolved_at_type_0
            except (TypeError, ValueError, AttributeError, KeyError):
                pass
            return cast(datetime.datetime | None, data)

        resolved_at = _parse_resolved_at(d.pop("resolved_at"))

        severity = check_status_incident_severity(d.pop("severity"))

        summary = d.pop("summary")

        component = d.pop("component", UNSET)

        status_incident = cls(
            started_at=started_at,
            resolved_at=resolved_at,
            severity=severity,
            summary=summary,
            component=component,
        )

        status_incident.additional_properties = d
        return status_incident

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
