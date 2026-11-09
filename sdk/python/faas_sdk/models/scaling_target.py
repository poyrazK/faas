from __future__ import annotations

from collections.abc import Mapping
from typing import TYPE_CHECKING, Any, TypeVar

from attrs import define as _attrs_define

from ..models.scaling_target_metric import ScalingTargetMetric, check_scaling_target_metric
from ..types import UNSET, Unset

if TYPE_CHECKING:
    from ..models.scaling_target_labels import ScalingTargetLabels


T = TypeVar("T", bound="ScalingTarget")


@_attrs_define
class ScalingTarget:
    """(metric, value) pair the engine watches for the scale-up trigger. The metric surface is closed, and every member of
    it is backed by a live source and read by a scheduler trigger. `p99_latency_ms` was removed: it validated for
    releases with no latency source behind it, and is now rejected with 422 pointing at concurrent_requests. The unset
    state (null) is the legacy 'engine falls back to autoscale_target_rps' path.

    """

    metric: ScalingTargetMetric | Unset = UNSET
    """rps = per-instance requests/second. cpu = max per-instance CPU percent. concurrent_requests = max per-
    instance in-flight requests. queue_depth = fleet backlog budget per worker."""
    name: str | Unset = UNSET
    """Which custom metric this target watches. Required when metric is `custom`, and REJECTED otherwise — a name
    on a platform-measured metric would be silently ignored, which is the accepted-but-inert shape this API keeps
    having to remove."""
    labels: ScalingTargetLabels | Unset = UNSET
    """custom targets only: watch the one labelled gauge series of the metric with exactly these labels instead of
    its aggregate. Keys match ^[a-z_][a-z0-9_]{0,62}$."""
    value: float | Unset = UNSET
    """Target value (units depend on Metric). Must be >= 0 in the singular `target` field for compatibility; inside
    `targets` it must be > 0. queue_depth requires a positive per-worker backlog budget, and cpu is capped at 100.
   """

    def to_dict(self) -> dict[str, Any]:
        metric: str | Unset = UNSET
        if not isinstance(self.metric, Unset):
            metric = self.metric

        name = self.name

        labels: dict[str, Any] | Unset = UNSET
        if not isinstance(self.labels, Unset):
            labels = self.labels.to_dict()

        value = self.value

        field_dict: dict[str, Any] = {}

        field_dict.update({})
        if metric is not UNSET:
            field_dict["metric"] = metric
        if name is not UNSET:
            field_dict["name"] = name
        if labels is not UNSET:
            field_dict["labels"] = labels
        if value is not UNSET:
            field_dict["value"] = value

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        from ..models.scaling_target_labels import ScalingTargetLabels

        d = dict(src_dict)
        _metric = d.pop("metric", UNSET)
        metric: ScalingTargetMetric | Unset
        if isinstance(_metric, Unset):
            metric = UNSET
        else:
            metric = check_scaling_target_metric(_metric)

        name = d.pop("name", UNSET)

        _labels = d.pop("labels", UNSET)
        labels: ScalingTargetLabels | Unset
        if isinstance(_labels, Unset):
            labels = UNSET
        else:
            labels = ScalingTargetLabels.from_dict(_labels)

        value = d.pop("value", UNSET)

        scaling_target = cls(
            metric=metric,
            name=name,
            labels=labels,
            value=value,
        )

        return scaling_target
