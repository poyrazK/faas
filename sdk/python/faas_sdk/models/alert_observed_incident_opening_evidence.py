from __future__ import annotations

from collections.abc import Mapping
from typing import Any, TypeVar

from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..models.alert_observed_incident_opening_evidence_comparison import (
    AlertObservedIncidentOpeningEvidenceComparison,
    check_alert_observed_incident_opening_evidence_comparison,
)
from ..types import UNSET, Unset

T = TypeVar("T", bound="AlertObservedIncidentOpeningEvidence")


@_attrs_define
class AlertObservedIncidentOpeningEvidence:
    """Values captured at the first breach of a live observed incident."""

    metric: str
    """Metric the rule evaluated when the incident opened."""
    window: str
    """Rolling metric window in use when the incident opened."""
    comparison: AlertObservedIncidentOpeningEvidenceComparison
    observed: float
    """Observed value in the metric's units."""
    baseline: float
    """Historical median in the metric's units."""
    trigger_value: float
    """Trigger derived from the baseline and rule threshold at opening."""
    baseline_days: int | Unset = UNSET
    """Number of days that qualified for the baseline."""
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        metric = self.metric

        window = self.window

        comparison: str = self.comparison

        observed = self.observed

        baseline = self.baseline

        trigger_value = self.trigger_value

        baseline_days = self.baseline_days

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "metric": metric,
                "window": window,
                "comparison": comparison,
                "observed": observed,
                "baseline": baseline,
                "trigger_value": trigger_value,
            }
        )
        if baseline_days is not UNSET:
            field_dict["baseline_days"] = baseline_days

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        metric = d.pop("metric")

        window = d.pop("window")

        comparison = check_alert_observed_incident_opening_evidence_comparison(d.pop("comparison"))

        observed = d.pop("observed")

        baseline = d.pop("baseline")

        trigger_value = d.pop("trigger_value")

        baseline_days = d.pop("baseline_days", UNSET)

        alert_observed_incident_opening_evidence = cls(
            metric=metric,
            window=window,
            comparison=comparison,
            observed=observed,
            baseline=baseline,
            trigger_value=trigger_value,
            baseline_days=baseline_days,
        )

        alert_observed_incident_opening_evidence.additional_properties = d
        return alert_observed_incident_opening_evidence

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
