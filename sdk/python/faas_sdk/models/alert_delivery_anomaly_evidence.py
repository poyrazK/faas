from __future__ import annotations

from collections.abc import Mapping
from typing import Any, TypeVar

from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..models.alert_delivery_anomaly_evidence_comparison import (
    AlertDeliveryAnomalyEvidenceComparison,
    check_alert_delivery_anomaly_evidence_comparison,
)
from ..models.alert_delivery_anomaly_evidence_metric import (
    AlertDeliveryAnomalyEvidenceMetric,
    check_alert_delivery_anomaly_evidence_metric,
)
from ..models.alert_delivery_anomaly_evidence_window import (
    AlertDeliveryAnomalyEvidenceWindow,
    check_alert_delivery_anomaly_evidence_window,
)

T = TypeVar("T", bound="AlertDeliveryAnomalyEvidence")


@_attrs_define
class AlertDeliveryAnomalyEvidence:
    """Baseline comparison captured at the time of an anomaly alert fire."""

    metric: AlertDeliveryAnomalyEvidenceMetric
    comparison: AlertDeliveryAnomalyEvidenceComparison
    threshold: float
    """ Configured multiplier when the alert fired. """
    window: AlertDeliveryAnomalyEvidenceWindow
    baseline: float
    """ Median value in the metric's units. """
    baseline_days: int
    """ Historical windows used in the baseline. """
    ratio: float
    """ Observed value divided by baseline. """
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        metric: str = self.metric

        comparison: str = self.comparison

        threshold = self.threshold

        window: str = self.window

        baseline = self.baseline

        baseline_days = self.baseline_days

        ratio = self.ratio

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "metric": metric,
                "comparison": comparison,
                "threshold": threshold,
                "window": window,
                "baseline": baseline,
                "baseline_days": baseline_days,
                "ratio": ratio,
            }
        )

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        metric = check_alert_delivery_anomaly_evidence_metric(d.pop("metric"))

        comparison = check_alert_delivery_anomaly_evidence_comparison(d.pop("comparison"))

        threshold = d.pop("threshold")

        window = check_alert_delivery_anomaly_evidence_window(d.pop("window"))

        baseline = d.pop("baseline")

        baseline_days = d.pop("baseline_days")

        ratio = d.pop("ratio")

        alert_delivery_anomaly_evidence = cls(
            metric=metric,
            comparison=comparison,
            threshold=threshold,
            window=window,
            baseline=baseline,
            baseline_days=baseline_days,
            ratio=ratio,
        )

        alert_delivery_anomaly_evidence.additional_properties = d
        return alert_delivery_anomaly_evidence

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
