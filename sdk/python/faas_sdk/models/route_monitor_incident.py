from __future__ import annotations

import datetime
from collections.abc import Mapping
from typing import TYPE_CHECKING, Any, TypeVar
from uuid import UUID

from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..models.route_monitor_incident_status import RouteMonitorIncidentStatus, check_route_monitor_incident_status
from ..types import UNSET, Unset

if TYPE_CHECKING:
    from ..models.route_monitor_evidence import RouteMonitorEvidence
    from ..models.route_monitor_report import RouteMonitorReport


T = TypeVar("T", bound="RouteMonitorIncident")


@_attrs_define
class RouteMonitorIncident:
    """Saved opening evidence, optional comparable recovery report and explicit incident lifecycle. Opening evidence stays
    fixed even when telemetry expires.

    """

    version: int
    id: UUID
    app_id: UUID
    deployment_id: UUID
    revision: int
    status: RouteMonitorIncidentStatus
    opened_at: datetime.datetime
    opening_report: RouteMonitorReport
    """Read-only absolute-budget evidence for the sole fully serving default-scope deployment. Coverage is limited
    to stored telemetry."""
    evidence: list[RouteMonitorEvidence]
    evidence_truncated: bool
    closed_at: datetime.datetime | Unset = UNSET
    recovery_report: RouteMonitorReport | Unset = UNSET
    """Read-only absolute-budget evidence for the sole fully serving default-scope deployment. Coverage is limited
    to stored telemetry."""
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        version = self.version

        id = str(self.id)

        app_id = str(self.app_id)

        deployment_id = str(self.deployment_id)

        revision = self.revision

        status: str = self.status

        opened_at = self.opened_at.isoformat()

        opening_report = self.opening_report.to_dict()

        evidence = []
        for evidence_item_data in self.evidence:
            evidence_item = evidence_item_data.to_dict()
            evidence.append(evidence_item)

        evidence_truncated = self.evidence_truncated

        closed_at: str | Unset = UNSET
        if not isinstance(self.closed_at, Unset):
            closed_at = self.closed_at.isoformat()

        recovery_report: dict[str, Any] | Unset = UNSET
        if not isinstance(self.recovery_report, Unset):
            recovery_report = self.recovery_report.to_dict()

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "version": version,
                "id": id,
                "app_id": app_id,
                "deployment_id": deployment_id,
                "revision": revision,
                "status": status,
                "opened_at": opened_at,
                "opening_report": opening_report,
                "evidence": evidence,
                "evidence_truncated": evidence_truncated,
            }
        )
        if closed_at is not UNSET:
            field_dict["closed_at"] = closed_at
        if recovery_report is not UNSET:
            field_dict["recovery_report"] = recovery_report

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        from ..models.route_monitor_evidence import RouteMonitorEvidence
        from ..models.route_monitor_report import RouteMonitorReport

        d = dict(src_dict)
        version = d.pop("version")

        id = UUID(d.pop("id"))

        app_id = UUID(d.pop("app_id"))

        deployment_id = UUID(d.pop("deployment_id"))

        revision = d.pop("revision")

        status = check_route_monitor_incident_status(d.pop("status"))

        opened_at = datetime.datetime.fromisoformat(d.pop("opened_at"))

        opening_report = RouteMonitorReport.from_dict(d.pop("opening_report"))

        evidence = []
        _evidence = d.pop("evidence")
        for evidence_item_data in _evidence:
            evidence_item = RouteMonitorEvidence.from_dict(evidence_item_data)

            evidence.append(evidence_item)

        evidence_truncated = d.pop("evidence_truncated")

        _closed_at = d.pop("closed_at", UNSET)
        closed_at: datetime.datetime | Unset
        if isinstance(_closed_at, Unset):
            closed_at = UNSET
        else:
            closed_at = datetime.datetime.fromisoformat(_closed_at)

        _recovery_report = d.pop("recovery_report", UNSET)
        recovery_report: RouteMonitorReport | Unset
        if isinstance(_recovery_report, Unset):
            recovery_report = UNSET
        else:
            recovery_report = RouteMonitorReport.from_dict(_recovery_report)

        route_monitor_incident = cls(
            version=version,
            id=id,
            app_id=app_id,
            deployment_id=deployment_id,
            revision=revision,
            status=status,
            opened_at=opened_at,
            opening_report=opening_report,
            evidence=evidence,
            evidence_truncated=evidence_truncated,
            closed_at=closed_at,
            recovery_report=recovery_report,
        )

        route_monitor_incident.additional_properties = d
        return route_monitor_incident

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
