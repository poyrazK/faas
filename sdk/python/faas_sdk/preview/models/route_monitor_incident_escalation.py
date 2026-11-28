from __future__ import annotations

import datetime
from collections.abc import Mapping
from typing import TYPE_CHECKING, Any, TypeVar
from uuid import UUID

from attrs import define as _attrs_define
from attrs import field as _attrs_field

if TYPE_CHECKING:
    from ..models.route_monitor_evidence import RouteMonitorEvidence
    from ..models.route_monitor_incident_escalation_signal import RouteMonitorIncidentEscalationSignal


T = TypeVar("T", bound="RouteMonitorIncidentEscalation")


@_attrs_define
class RouteMonitorIncidentEscalation:
    """One stable route-monitor escalation transition. It preserves the newly violated signals and up to three fresh
    aggregate request/latency diagnostic entries; no customer identifiers are included.

    """

    transition_id: UUID
    """Same stable identifier as the corresponding routes.monitor.escalated outbox event."""
    checked_at: datetime.datetime
    previous_checked_at: datetime.datetime
    newly_violated_routes: int
    newly_violated_signals: int
    signals: list[RouteMonitorIncidentEscalationSignal]
    evidence: list[RouteMonitorEvidence]
    evidence_truncated: bool
    """True when more signals changed than the three-entry evidence cap, or incident-size compaction removed
    evidence."""
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        transition_id = str(self.transition_id)

        checked_at = self.checked_at.isoformat()

        previous_checked_at = self.previous_checked_at.isoformat()

        newly_violated_routes = self.newly_violated_routes

        newly_violated_signals = self.newly_violated_signals

        signals = []
        for signals_item_data in self.signals:
            signals_item = signals_item_data.to_dict()
            signals.append(signals_item)

        evidence = []
        for evidence_item_data in self.evidence:
            evidence_item = evidence_item_data.to_dict()
            evidence.append(evidence_item)

        evidence_truncated = self.evidence_truncated

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "transition_id": transition_id,
                "checked_at": checked_at,
                "previous_checked_at": previous_checked_at,
                "newly_violated_routes": newly_violated_routes,
                "newly_violated_signals": newly_violated_signals,
                "signals": signals,
                "evidence": evidence,
                "evidence_truncated": evidence_truncated,
            }
        )

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        from ..models.route_monitor_evidence import RouteMonitorEvidence
        from ..models.route_monitor_incident_escalation_signal import RouteMonitorIncidentEscalationSignal

        d = dict(src_dict)
        transition_id = UUID(d.pop("transition_id"))

        checked_at = datetime.datetime.fromisoformat(d.pop("checked_at"))

        previous_checked_at = datetime.datetime.fromisoformat(d.pop("previous_checked_at"))

        newly_violated_routes = d.pop("newly_violated_routes")

        newly_violated_signals = d.pop("newly_violated_signals")

        signals = []
        _signals = d.pop("signals")
        for signals_item_data in _signals:
            signals_item = RouteMonitorIncidentEscalationSignal.from_dict(signals_item_data)

            signals.append(signals_item)

        evidence = []
        _evidence = d.pop("evidence")
        for evidence_item_data in _evidence:
            evidence_item = RouteMonitorEvidence.from_dict(evidence_item_data)

            evidence.append(evidence_item)

        evidence_truncated = d.pop("evidence_truncated")

        route_monitor_incident_escalation = cls(
            transition_id=transition_id,
            checked_at=checked_at,
            previous_checked_at=previous_checked_at,
            newly_violated_routes=newly_violated_routes,
            newly_violated_signals=newly_violated_signals,
            signals=signals,
            evidence=evidence,
            evidence_truncated=evidence_truncated,
        )

        route_monitor_incident_escalation.additional_properties = d
        return route_monitor_incident_escalation

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
