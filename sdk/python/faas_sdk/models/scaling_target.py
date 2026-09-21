from __future__ import annotations

from collections.abc import Mapping
from typing import Any, TypeVar

from attrs import define as _attrs_define

from ..models.scaling_target_metric import ScalingTargetMetric, check_scaling_target_metric
from ..types import UNSET, Unset

T = TypeVar("T", bound="ScalingTarget")


@_attrs_define
class ScalingTarget:
    """(metric, value) pair the engine watches for the scale-up trigger. The metric surface is closed, and since ADR-194
    every member of it is backed by a live source and read by a scheduler trigger. `p99_latency_ms` was removed: it
    validated for releases with no latency source behind it, and is now rejected with 422 pointing at
    concurrent_requests. The unset state (null) is the legacy 'engine falls back to autoscale_target_rps' path.

    """

    metric: ScalingTargetMetric | Unset = UNSET
    """rps = per-instance requests/second. cpu = max per-instance CPU percent. concurrent_requests = max per-
    instance in-flight requests. queue_depth = fleet backlog budget per worker."""
    value: float | Unset = UNSET
    """Target value (units depend on Metric). Must be >= 0 in the singular `target` field for compatibility; inside
    `targets` it must be > 0. queue_depth requires a positive per-worker backlog budget, and cpu is capped at 100.
   """

    def to_dict(self) -> dict[str, Any]:
        metric: str | Unset = UNSET
        if not isinstance(self.metric, Unset):
            metric = self.metric

        value = self.value

        field_dict: dict[str, Any] = {}

        field_dict.update({})
        if metric is not UNSET:
            field_dict["metric"] = metric
        if value is not UNSET:
            field_dict["value"] = value

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        _metric = d.pop("metric", UNSET)
        metric: ScalingTargetMetric | Unset
        if isinstance(_metric, Unset):
            metric = UNSET
        else:
            metric = check_scaling_target_metric(_metric)

        value = d.pop("value", UNSET)

        scaling_target = cls(
            metric=metric,
            value=value,
        )

        return scaling_target
