from __future__ import annotations

import datetime
from collections.abc import Mapping
from typing import Any, TypeVar

from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..models.alert_anomaly_evaluation_reason import AlertAnomalyEvaluationReason, check_alert_anomaly_evaluation_reason
from ..types import UNSET, Unset

T = TypeVar("T", bound="AlertAnomalyEvaluation")


@_attrs_define
class AlertAnomalyEvaluation:
    """Why the latest anomaly evaluation did not trigger a fire."""

    reason: AlertAnomalyEvaluationReason
    evaluated_at: datetime.datetime
    count: int
    """ Eligible requests, qualifying historical windows, or consecutive healthy checks depending on the reason. """
    baseline_days: int | Unset = UNSET
    """ Number of days that qualified for the baseline when history was evaluated. """
    required_count: int | Unset = UNSET
    """ Required requests, historical windows, or healthy checks when the reason includes a count. """
    observed: float | Unset = UNSET
    """ Observed value in the rule metric's units for a completed check. """
    baseline: float | Unset = UNSET
    """ Historical median in the rule metric's units for a completed check. """
    trigger_value: float | Unset = UNSET
    """ Baseline multiplied by the rule threshold when a positive baseline is available. """
    minimum_value: float | Unset = UNSET
    """ Minimum required observation for an above-baseline rule, or minimum required baseline for a below-baseline
    rule. Present when that minimum was not reached. """
    next_notification_eligible_at: datetime.datetime | Unset = UNSET
    """ End of the current incident's notification cooldown when the evaluation suppressed a repeat fire. """
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        reason: str = self.reason

        evaluated_at = self.evaluated_at.isoformat()

        count = self.count

        baseline_days = self.baseline_days

        required_count = self.required_count

        observed = self.observed

        baseline = self.baseline

        trigger_value = self.trigger_value

        minimum_value = self.minimum_value

        next_notification_eligible_at: str | Unset = UNSET
        if not isinstance(self.next_notification_eligible_at, Unset):
            next_notification_eligible_at = self.next_notification_eligible_at.isoformat()

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "reason": reason,
                "evaluated_at": evaluated_at,
                "count": count,
            }
        )
        if baseline_days is not UNSET:
            field_dict["baseline_days"] = baseline_days
        if required_count is not UNSET:
            field_dict["required_count"] = required_count
        if observed is not UNSET:
            field_dict["observed"] = observed
        if baseline is not UNSET:
            field_dict["baseline"] = baseline
        if trigger_value is not UNSET:
            field_dict["trigger_value"] = trigger_value
        if minimum_value is not UNSET:
            field_dict["minimum_value"] = minimum_value
        if next_notification_eligible_at is not UNSET:
            field_dict["next_notification_eligible_at"] = next_notification_eligible_at

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        reason = check_alert_anomaly_evaluation_reason(d.pop("reason"))

        evaluated_at = datetime.datetime.fromisoformat(d.pop("evaluated_at"))

        count = d.pop("count")

        baseline_days = d.pop("baseline_days", UNSET)

        required_count = d.pop("required_count", UNSET)

        observed = d.pop("observed", UNSET)

        baseline = d.pop("baseline", UNSET)

        trigger_value = d.pop("trigger_value", UNSET)

        minimum_value = d.pop("minimum_value", UNSET)

        _next_notification_eligible_at = d.pop("next_notification_eligible_at", UNSET)
        next_notification_eligible_at: datetime.datetime | Unset
        if isinstance(_next_notification_eligible_at, Unset):
            next_notification_eligible_at = UNSET
        else:
            next_notification_eligible_at = datetime.datetime.fromisoformat(_next_notification_eligible_at)

        alert_anomaly_evaluation = cls(
            reason=reason,
            evaluated_at=evaluated_at,
            count=count,
            baseline_days=baseline_days,
            required_count=required_count,
            observed=observed,
            baseline=baseline,
            trigger_value=trigger_value,
            minimum_value=minimum_value,
            next_notification_eligible_at=next_notification_eligible_at,
        )

        alert_anomaly_evaluation.additional_properties = d
        return alert_anomaly_evaluation

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
