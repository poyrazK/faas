from __future__ import annotations

import datetime
from collections.abc import Mapping
from typing import Any, TypeVar

from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..models.release_gate_comparison import ReleaseGateComparison, check_release_gate_comparison
from ..models.release_gate_kind import ReleaseGateKind, check_release_gate_kind
from ..types import UNSET, Unset

T = TypeVar("T", bound="ReleaseGate")


@_attrs_define
class ReleaseGate:
    """One of an app's release gates (ADR-20261019071734, ADR-20261022082305)."""

    name: str
    kind: ReleaseGateKind
    min_samples: int
    """Requests, probes or metric values a release needs before the gate is judged."""
    created_by: str
    created_at: datetime.datetime
    slo: str | Unset = UNSET
    """slo gates: the SLO judged."""
    max_burn_rate: int | Unset = UNSET
    """slo gates: the burn rate that fails the gate."""
    max_new_groups: int | Unset = UNSET
    """new_errors gates: new error groups allowed."""
    min_events: int | Unset = UNSET
    """new_errors gates: events before a group counts."""
    metric: str | Unset = UNSET
    """custom_metric and metric_change gates: the custom metric."""
    comparison: ReleaseGateComparison | Unset = UNSET
    """custom_metric gates: which side of the threshold fails; metric_change gates: which direction of change
    fails."""
    threshold: float | Unset = UNSET
    """custom_metric gates: the bound on the metric's average."""
    check: str | Unset = UNSET
    """synthetic gates: the synthetic check probed on the canary and its stable revision."""
    max_failure_percent: int | Unset = UNSET
    """synthetic gates: the share of the candidate's probes that may fail."""
    max_change_percent: int | Unset = UNSET
    """metric_change gates: how far the metric may move from its average before the rollout, in percent."""
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        name = self.name

        kind: str = self.kind

        min_samples = self.min_samples

        created_by = self.created_by

        created_at = self.created_at.isoformat()

        slo = self.slo

        max_burn_rate = self.max_burn_rate

        max_new_groups = self.max_new_groups

        min_events = self.min_events

        metric = self.metric

        comparison: str | Unset = UNSET
        if not isinstance(self.comparison, Unset):
            comparison = self.comparison

        threshold = self.threshold

        check = self.check

        max_failure_percent = self.max_failure_percent

        max_change_percent = self.max_change_percent

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "name": name,
                "kind": kind,
                "min_samples": min_samples,
                "created_by": created_by,
                "created_at": created_at,
            }
        )
        if slo is not UNSET:
            field_dict["slo"] = slo
        if max_burn_rate is not UNSET:
            field_dict["max_burn_rate"] = max_burn_rate
        if max_new_groups is not UNSET:
            field_dict["max_new_groups"] = max_new_groups
        if min_events is not UNSET:
            field_dict["min_events"] = min_events
        if metric is not UNSET:
            field_dict["metric"] = metric
        if comparison is not UNSET:
            field_dict["comparison"] = comparison
        if threshold is not UNSET:
            field_dict["threshold"] = threshold
        if check is not UNSET:
            field_dict["check"] = check
        if max_failure_percent is not UNSET:
            field_dict["max_failure_percent"] = max_failure_percent
        if max_change_percent is not UNSET:
            field_dict["max_change_percent"] = max_change_percent

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        name = d.pop("name")

        kind = check_release_gate_kind(d.pop("kind"))

        min_samples = d.pop("min_samples")

        created_by = d.pop("created_by")

        created_at = datetime.datetime.fromisoformat(d.pop("created_at"))

        slo = d.pop("slo", UNSET)

        max_burn_rate = d.pop("max_burn_rate", UNSET)

        max_new_groups = d.pop("max_new_groups", UNSET)

        min_events = d.pop("min_events", UNSET)

        metric = d.pop("metric", UNSET)

        _comparison = d.pop("comparison", UNSET)
        comparison: ReleaseGateComparison | Unset
        if isinstance(_comparison, Unset):
            comparison = UNSET
        else:
            comparison = check_release_gate_comparison(_comparison)

        threshold = d.pop("threshold", UNSET)

        check = d.pop("check", UNSET)

        max_failure_percent = d.pop("max_failure_percent", UNSET)

        max_change_percent = d.pop("max_change_percent", UNSET)

        release_gate = cls(
            name=name,
            kind=kind,
            min_samples=min_samples,
            created_by=created_by,
            created_at=created_at,
            slo=slo,
            max_burn_rate=max_burn_rate,
            max_new_groups=max_new_groups,
            min_events=min_events,
            metric=metric,
            comparison=comparison,
            threshold=threshold,
            check=check,
            max_failure_percent=max_failure_percent,
            max_change_percent=max_change_percent,
        )

        release_gate.additional_properties = d
        return release_gate

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
