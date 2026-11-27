from __future__ import annotations

from collections.abc import Mapping
from typing import TYPE_CHECKING, Any, TypeVar

from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..types import UNSET, Unset

if TYPE_CHECKING:
    from ..models.alert_anomaly_deployment_context import AlertAnomalyDeploymentContext
    from ..models.alert_anomaly_recovery_comparison import AlertAnomalyRecoveryComparison
    from ..models.alert_observed_incident import AlertObservedIncident


T = TypeVar("T", bound="AlertAnomalyIncidentReplay")


@_attrs_define
class AlertAnomalyIncidentReplay:
    """Anomaly incidents reconstructed from a selected two-hour interval."""

    cooldown_minutes: int
    incidents: list[AlertObservedIncident]
    """Incidents for the selected multiplier, newest first. An incident is open at the end of the replay when
    ended_at is absent."""
    candidate_incidents: list[AlertObservedIncident] | Unset = UNSET
    """Incidents for the candidate, newest first. Omitted when the candidate has no incidents or is absent."""
    recovery_confirmation: AlertAnomalyRecoveryComparison | Unset = UNSET
    """Replay outcomes with two consecutive healthy checks required for recovery."""
    deployment_context: AlertAnomalyDeploymentContext | Unset = UNSET
    """Recorded full-traffic promotions for the selected environment. An app-wide replay includes default-
    environment promotions."""
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        cooldown_minutes = self.cooldown_minutes

        incidents = []
        for incidents_item_data in self.incidents:
            incidents_item = incidents_item_data.to_dict()
            incidents.append(incidents_item)

        candidate_incidents: list[dict[str, Any]] | Unset = UNSET
        if not isinstance(self.candidate_incidents, Unset):
            candidate_incidents = []
            for candidate_incidents_item_data in self.candidate_incidents:
                candidate_incidents_item = candidate_incidents_item_data.to_dict()
                candidate_incidents.append(candidate_incidents_item)

        recovery_confirmation: dict[str, Any] | Unset = UNSET
        if not isinstance(self.recovery_confirmation, Unset):
            recovery_confirmation = self.recovery_confirmation.to_dict()

        deployment_context: dict[str, Any] | Unset = UNSET
        if not isinstance(self.deployment_context, Unset):
            deployment_context = self.deployment_context.to_dict()

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "cooldown_minutes": cooldown_minutes,
                "incidents": incidents,
            }
        )
        if candidate_incidents is not UNSET:
            field_dict["candidate_incidents"] = candidate_incidents
        if recovery_confirmation is not UNSET:
            field_dict["recovery_confirmation"] = recovery_confirmation
        if deployment_context is not UNSET:
            field_dict["deployment_context"] = deployment_context

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        from ..models.alert_anomaly_deployment_context import AlertAnomalyDeploymentContext
        from ..models.alert_anomaly_recovery_comparison import AlertAnomalyRecoveryComparison
        from ..models.alert_observed_incident import AlertObservedIncident

        d = dict(src_dict)
        cooldown_minutes = d.pop("cooldown_minutes")

        incidents = []
        _incidents = d.pop("incidents")
        for incidents_item_data in _incidents:
            incidents_item = AlertObservedIncident.from_dict(incidents_item_data)

            incidents.append(incidents_item)

        _candidate_incidents = d.pop("candidate_incidents", UNSET)
        candidate_incidents: list[AlertObservedIncident] | Unset = UNSET
        if _candidate_incidents is not UNSET:
            candidate_incidents = []
            for candidate_incidents_item_data in _candidate_incidents:
                candidate_incidents_item = AlertObservedIncident.from_dict(candidate_incidents_item_data)

                candidate_incidents.append(candidate_incidents_item)

        _recovery_confirmation = d.pop("recovery_confirmation", UNSET)
        recovery_confirmation: AlertAnomalyRecoveryComparison | Unset
        if isinstance(_recovery_confirmation, Unset):
            recovery_confirmation = UNSET
        else:
            recovery_confirmation = AlertAnomalyRecoveryComparison.from_dict(_recovery_confirmation)

        _deployment_context = d.pop("deployment_context", UNSET)
        deployment_context: AlertAnomalyDeploymentContext | Unset
        if isinstance(_deployment_context, Unset):
            deployment_context = UNSET
        else:
            deployment_context = AlertAnomalyDeploymentContext.from_dict(_deployment_context)

        alert_anomaly_incident_replay = cls(
            cooldown_minutes=cooldown_minutes,
            incidents=incidents,
            candidate_incidents=candidate_incidents,
            recovery_confirmation=recovery_confirmation,
            deployment_context=deployment_context,
        )

        alert_anomaly_incident_replay.additional_properties = d
        return alert_anomaly_incident_replay

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
