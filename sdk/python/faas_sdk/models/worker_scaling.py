from __future__ import annotations

from collections.abc import Mapping
from typing import Any, TypeVar

from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..models.worker_scaling_metric import WorkerScalingMetric, check_worker_scaling_metric

T = TypeVar("T", bound="WorkerScaling")


@_attrs_define
class WorkerScaling:
    """Queue-driven autoscaling policy for execution_mode='worker'. Supports scale-to-zero when min=0."""

    min_: int
    """Minimum worker instances to maintain. 0 enables scale-to-zero."""
    max_: int
    """Maximum worker instances (bounded by plan WorkerReplicasMax and app max_concurrency)."""
    metric: WorkerScalingMetric
    """Queue metric driving autoscaling."""
    target: float
    """Target backlog per worker instance (e.g. 500 messages per worker)."""
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        min_ = self.min_

        max_ = self.max_

        metric: str = self.metric

        target = self.target

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "min": min_,
                "max": max_,
                "metric": metric,
                "target": target,
            }
        )

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        min_ = d.pop("min")

        max_ = d.pop("max")

        metric = check_worker_scaling_metric(d.pop("metric"))

        target = d.pop("target")

        worker_scaling = cls(
            min_=min_,
            max_=max_,
            metric=metric,
            target=target,
        )

        worker_scaling.additional_properties = d
        return worker_scaling

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
