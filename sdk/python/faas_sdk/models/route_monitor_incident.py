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
    from ..models.route_monitor_deployment_baseline import RouteMonitorDeploymentBaseline
    from ..models.route_monitor_evidence import RouteMonitorEvidence
    from ..models.route_monitor_incident_escalation import RouteMonitorIncidentEscalation
    from ..models.route_monitor_incident_rollback import RouteMonitorIncidentRollback
    from ..models.route_monitor_incident_timeline_entry import RouteMonitorIncidentTimelineEntry
    from ..models.route_monitor_report import RouteMonitorReport


T = TypeVar("T", bound="RouteMonitorIncident")


@_attrs_define
class RouteMonitorIncident:
    """Saved opening evidence, the previous healthy deployment when known, bounded route-impact timeline, transition-linked
    escalation evidence, optional comparable recovery report and explicit incident lifecycle. Opening evidence stays
    fixed even when telemetry expires. Timeline entries contain aggregate customer counts only and preserve the opening
    baseline plus the newest evaluations.

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
    baseline: RouteMonitorDeploymentBaseline | Unset = UNSET
    """The last different fully serving deployment with a healthy route-monitor report before this incident opened.
    Provenance values are sanitized declared metadata and do not attest to deployment archive bytes."""
    recovery_report: RouteMonitorReport | Unset = UNSET
    """Read-only absolute-budget evidence for the sole fully serving default-scope deployment. Coverage is limited
    to stored telemetry."""
    timeline: list[RouteMonitorIncidentTimelineEntry] | Unset = UNSET
    """Opening baseline and up to 59 most recent confirmed evaluations. Route indexes refer to
    opening_report.routes."""
    timeline_truncated: bool | Unset = UNSET
    """True when older evaluations were dropped to preserve the opening baseline and bounded incident size."""
    escalations: list[RouteMonitorIncidentEscalation] | Unset = UNSET
    """Newest newly violated route/signal transitions with bounded evidence captured at each transition."""
    escalations_truncated: bool | Unset = UNSET
    """True when older escalation records were dropped to preserve the newest transition details and incident size."""
    rollback: RouteMonitorIncidentRollback | Unset = UNSET
    """The single automatic rollback decision for an incident when on_violation is rollback (ADR-845). claimed is
    transient while the checked rollback is requested."""
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

        baseline: dict[str, Any] | Unset = UNSET
        if not isinstance(self.baseline, Unset):
            baseline = self.baseline.to_dict()

        recovery_report: dict[str, Any] | Unset = UNSET
        if not isinstance(self.recovery_report, Unset):
            recovery_report = self.recovery_report.to_dict()

        timeline: list[dict[str, Any]] | Unset = UNSET
        if not isinstance(self.timeline, Unset):
            timeline = []
            for timeline_item_data in self.timeline:
                timeline_item = timeline_item_data.to_dict()
                timeline.append(timeline_item)

        timeline_truncated = self.timeline_truncated

        escalations: list[dict[str, Any]] | Unset = UNSET
        if not isinstance(self.escalations, Unset):
            escalations = []
            for escalations_item_data in self.escalations:
                escalations_item = escalations_item_data.to_dict()
                escalations.append(escalations_item)

        escalations_truncated = self.escalations_truncated

        rollback: dict[str, Any] | Unset = UNSET
        if not isinstance(self.rollback, Unset):
            rollback = self.rollback.to_dict()

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
        if baseline is not UNSET:
            field_dict["baseline"] = baseline
        if recovery_report is not UNSET:
            field_dict["recovery_report"] = recovery_report
        if timeline is not UNSET:
            field_dict["timeline"] = timeline
        if timeline_truncated is not UNSET:
            field_dict["timeline_truncated"] = timeline_truncated
        if escalations is not UNSET:
            field_dict["escalations"] = escalations
        if escalations_truncated is not UNSET:
            field_dict["escalations_truncated"] = escalations_truncated
        if rollback is not UNSET:
            field_dict["rollback"] = rollback

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        from ..models.route_monitor_deployment_baseline import RouteMonitorDeploymentBaseline
        from ..models.route_monitor_evidence import RouteMonitorEvidence
        from ..models.route_monitor_incident_escalation import RouteMonitorIncidentEscalation
        from ..models.route_monitor_incident_rollback import RouteMonitorIncidentRollback
        from ..models.route_monitor_incident_timeline_entry import RouteMonitorIncidentTimelineEntry
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

        _baseline = d.pop("baseline", UNSET)
        baseline: RouteMonitorDeploymentBaseline | Unset
        if isinstance(_baseline, Unset):
            baseline = UNSET
        else:
            baseline = RouteMonitorDeploymentBaseline.from_dict(_baseline)

        _recovery_report = d.pop("recovery_report", UNSET)
        recovery_report: RouteMonitorReport | Unset
        if isinstance(_recovery_report, Unset):
            recovery_report = UNSET
        else:
            recovery_report = RouteMonitorReport.from_dict(_recovery_report)

        _timeline = d.pop("timeline", UNSET)
        timeline: list[RouteMonitorIncidentTimelineEntry] | Unset = UNSET
        if _timeline is not UNSET:
            timeline = []
            for timeline_item_data in _timeline:
                timeline_item = RouteMonitorIncidentTimelineEntry.from_dict(timeline_item_data)

                timeline.append(timeline_item)

        timeline_truncated = d.pop("timeline_truncated", UNSET)

        _escalations = d.pop("escalations", UNSET)
        escalations: list[RouteMonitorIncidentEscalation] | Unset = UNSET
        if _escalations is not UNSET:
            escalations = []
            for escalations_item_data in _escalations:
                escalations_item = RouteMonitorIncidentEscalation.from_dict(escalations_item_data)

                escalations.append(escalations_item)

        escalations_truncated = d.pop("escalations_truncated", UNSET)

        _rollback = d.pop("rollback", UNSET)
        rollback: RouteMonitorIncidentRollback | Unset
        if isinstance(_rollback, Unset):
            rollback = UNSET
        else:
            rollback = RouteMonitorIncidentRollback.from_dict(_rollback)

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
            baseline=baseline,
            recovery_report=recovery_report,
            timeline=timeline,
            timeline_truncated=timeline_truncated,
            escalations=escalations,
            escalations_truncated=escalations_truncated,
            rollback=rollback,
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
