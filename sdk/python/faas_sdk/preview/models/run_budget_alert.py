from __future__ import annotations

import datetime
from collections.abc import Mapping
from typing import Any, TypeVar
from uuid import UUID

from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..models.run_budget_alert_metric import RunBudgetAlertMetric, check_run_budget_alert_metric
from ..models.run_budget_alert_threshold_percent import (
    RunBudgetAlertThresholdPercent,
    check_run_budget_alert_threshold_percent,
)
from ..models.run_budget_kind import RunBudgetKind, check_run_budget_kind

T = TypeVar("T", bound="RunBudgetAlert")


@_attrs_define
class RunBudgetAlert:
    """Recorded customer or task budget threshold event."""

    event_id: UUID
    kind: RunBudgetKind
    """Whether a budget applies to one customer or a named task."""
    ref: str
    day: datetime.date
    metric: RunBudgetAlertMetric
    threshold_percent: RunBudgetAlertThresholdPercent
    limit: int
    observed: int
    created_at: datetime.datetime
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        event_id = str(self.event_id)

        kind: str = self.kind

        ref = self.ref

        day = self.day.isoformat()

        metric: str = self.metric

        threshold_percent: int = self.threshold_percent

        limit = self.limit

        observed = self.observed

        created_at = self.created_at.isoformat()

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "event_id": event_id,
                "kind": kind,
                "ref": ref,
                "day": day,
                "metric": metric,
                "threshold_percent": threshold_percent,
                "limit": limit,
                "observed": observed,
                "created_at": created_at,
            }
        )

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        event_id = UUID(d.pop("event_id"))

        kind = check_run_budget_kind(d.pop("kind"))

        ref = d.pop("ref")

        day = datetime.date.fromisoformat(d.pop("day"))

        metric = check_run_budget_alert_metric(d.pop("metric"))

        threshold_percent = check_run_budget_alert_threshold_percent(d.pop("threshold_percent"))

        limit = d.pop("limit")

        observed = d.pop("observed")

        created_at = datetime.datetime.fromisoformat(d.pop("created_at"))

        run_budget_alert = cls(
            event_id=event_id,
            kind=kind,
            ref=ref,
            day=day,
            metric=metric,
            threshold_percent=threshold_percent,
            limit=limit,
            observed=observed,
            created_at=created_at,
        )

        run_budget_alert.additional_properties = d
        return run_budget_alert

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
