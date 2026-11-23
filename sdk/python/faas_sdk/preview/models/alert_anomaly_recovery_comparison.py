from __future__ import annotations

from collections.abc import Mapping
from typing import TYPE_CHECKING, Any, TypeVar

from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..models.alert_anomaly_recovery_comparison_required_healthy_checks import (
    AlertAnomalyRecoveryComparisonRequiredHealthyChecks,
    check_alert_anomaly_recovery_comparison_required_healthy_checks,
)
from ..types import UNSET, Unset

if TYPE_CHECKING:
    from ..models.alert_observed_incident import AlertObservedIncident


T = TypeVar("T", bound="AlertAnomalyRecoveryComparison")


@_attrs_define
class AlertAnomalyRecoveryComparison:
    """Replay outcomes with two consecutive healthy checks required for recovery."""

    required_healthy_checks: AlertAnomalyRecoveryComparisonRequiredHealthyChecks
    incidents: list[AlertObservedIncident]
    """Incidents for the selected multiplier with recovery confirmation, newest first."""
    candidate_incidents: list[AlertObservedIncident] | Unset = UNSET
    """Incidents for the candidate with recovery confirmation, newest first. Omitted when the candidate has no
    incidents or is absent."""
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        required_healthy_checks: int = self.required_healthy_checks

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

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "required_healthy_checks": required_healthy_checks,
                "incidents": incidents,
            }
        )
        if candidate_incidents is not UNSET:
            field_dict["candidate_incidents"] = candidate_incidents

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        from ..models.alert_observed_incident import AlertObservedIncident

        d = dict(src_dict)
        required_healthy_checks = check_alert_anomaly_recovery_comparison_required_healthy_checks(
            d.pop("required_healthy_checks")
        )

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

        alert_anomaly_recovery_comparison = cls(
            required_healthy_checks=required_healthy_checks,
            incidents=incidents,
            candidate_incidents=candidate_incidents,
        )

        alert_anomaly_recovery_comparison.additional_properties = d
        return alert_anomaly_recovery_comparison

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
