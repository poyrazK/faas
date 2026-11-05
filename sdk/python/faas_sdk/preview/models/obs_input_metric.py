from __future__ import annotations

from collections.abc import Mapping
from typing import TYPE_CHECKING, Any, TypeVar, cast

from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..models.obs_input_metric_metric import ObsInputMetricMetric, check_obs_input_metric_metric
from ..models.obs_input_metric_reason import ObsInputMetricReason, check_obs_input_metric_reason
from ..models.obs_input_metric_state import ObsInputMetricState, check_obs_input_metric_state
from ..types import UNSET, Unset

if TYPE_CHECKING:
    from ..models.obs_input_metric_counts import ObsInputMetricCounts


T = TypeVar("T", bound="ObsInputMetric")


@_attrs_define
class ObsInputMetric:
    """Presence and freshness of one idle-safe readiness gauge on each fresh successful scrape target."""

    metric: ObsInputMetricMetric
    state: ObsInputMetricState
    counts: None | ObsInputMetricCounts
    """Gauge sample partition over all fresh up targets; null if this metric assessment is unknown."""
    reason: ObsInputMetricReason | Unset = UNSET
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        from ..models.obs_input_metric_counts import ObsInputMetricCounts

        metric: str = self.metric

        state: str = self.state

        counts: dict[str, Any] | None
        if isinstance(self.counts, ObsInputMetricCounts):
            counts = self.counts.to_dict()
        else:
            counts = self.counts

        reason: str | Unset = UNSET
        if not isinstance(self.reason, Unset):
            reason = self.reason

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "metric": metric,
                "state": state,
                "counts": counts,
            }
        )
        if reason is not UNSET:
            field_dict["reason"] = reason

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        from ..models.obs_input_metric_counts import ObsInputMetricCounts

        d = dict(src_dict)
        metric = check_obs_input_metric_metric(d.pop("metric"))

        state = check_obs_input_metric_state(d.pop("state"))

        def _parse_counts(data: object) -> None | ObsInputMetricCounts:
            if data is None:
                return data
            try:
                if not isinstance(data, dict):
                    raise TypeError()
                counts_type_0 = ObsInputMetricCounts.from_dict(data)

                return counts_type_0
            except (TypeError, ValueError, AttributeError, KeyError):
                pass
            return cast(None | ObsInputMetricCounts, data)

        counts = _parse_counts(d.pop("counts"))

        _reason = d.pop("reason", UNSET)
        reason: ObsInputMetricReason | Unset
        if isinstance(_reason, Unset):
            reason = UNSET
        else:
            reason = check_obs_input_metric_reason(_reason)

        obs_input_metric = cls(
            metric=metric,
            state=state,
            counts=counts,
            reason=reason,
        )

        obs_input_metric.additional_properties = d
        return obs_input_metric

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
