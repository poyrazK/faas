from __future__ import annotations

import datetime
from collections.abc import Mapping
from typing import Any, TypeVar

from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..models.obs_alert_workload_oldest_severity import (
    ObsAlertWorkloadOldestSeverity,
    check_obs_alert_workload_oldest_severity,
)
from ..models.obs_alert_workload_oldest_status import (
    ObsAlertWorkloadOldestStatus,
    check_obs_alert_workload_oldest_status,
)

T = TypeVar("T", bound="ObsAlertWorkloadOldest")


@_attrs_define
class ObsAlertWorkloadOldest:
    """Sanitized oldest eligible episode reference ordered by activation time then immutable ID."""

    incident_id: str
    status: ObsAlertWorkloadOldestStatus
    severity: ObsAlertWorkloadOldestSeverity
    started_at: datetime.datetime
    age_seconds: int
    evidence_path: str
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        incident_id = self.incident_id

        status: str = self.status

        severity: str = self.severity

        started_at = self.started_at.isoformat()

        age_seconds = self.age_seconds

        evidence_path = self.evidence_path

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "incident_id": incident_id,
                "status": status,
                "severity": severity,
                "started_at": started_at,
                "age_seconds": age_seconds,
                "evidence_path": evidence_path,
            }
        )

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        incident_id = d.pop("incident_id")

        status = check_obs_alert_workload_oldest_status(d.pop("status"))

        severity = check_obs_alert_workload_oldest_severity(d.pop("severity"))

        started_at = datetime.datetime.fromisoformat(d.pop("started_at"))

        age_seconds = d.pop("age_seconds")

        evidence_path = d.pop("evidence_path")

        obs_alert_workload_oldest = cls(
            incident_id=incident_id,
            status=status,
            severity=severity,
            started_at=started_at,
            age_seconds=age_seconds,
            evidence_path=evidence_path,
        )

        obs_alert_workload_oldest.additional_properties = d
        return obs_alert_workload_oldest

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
