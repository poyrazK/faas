from __future__ import annotations

from collections.abc import Mapping
from typing import TYPE_CHECKING, Any, TypeVar

from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..types import UNSET, Unset

if TYPE_CHECKING:
    from ..models.alert_anomaly_preview_breach import AlertAnomalyPreviewBreach
    from ..models.alert_anomaly_weekday_preview import AlertAnomalyWeekdayPreview
    from ..models.alert_anomaly_zero_baseline_fallback import AlertAnomalyZeroBaselineFallback


T = TypeVar("T", bound="AlertAnomalyPreviewCandidate")


@_attrs_define
class AlertAnomalyPreviewCandidate:
    """Alternate threshold or baseline evaluated on the preview checkpoints."""

    threshold: float
    """Candidate baseline multiplier."""
    breaches: list[AlertAnomalyPreviewBreach]
    weekday_baseline: AlertAnomalyWeekdayPreview | Unset = UNSET
    """Coverage for the same-weekday candidate."""
    zero_baseline_fallback: AlertAnomalyZeroBaselineFallback | Unset = UNSET
    """Absolute error-rate and failure-count guard for a zero historical baseline."""
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        threshold = self.threshold

        breaches = []
        for breaches_item_data in self.breaches:
            breaches_item = breaches_item_data.to_dict()
            breaches.append(breaches_item)

        weekday_baseline: dict[str, Any] | Unset = UNSET
        if not isinstance(self.weekday_baseline, Unset):
            weekday_baseline = self.weekday_baseline.to_dict()

        zero_baseline_fallback: dict[str, Any] | Unset = UNSET
        if not isinstance(self.zero_baseline_fallback, Unset):
            zero_baseline_fallback = self.zero_baseline_fallback.to_dict()

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "threshold": threshold,
                "breaches": breaches,
            }
        )
        if weekday_baseline is not UNSET:
            field_dict["weekday_baseline"] = weekday_baseline
        if zero_baseline_fallback is not UNSET:
            field_dict["zero_baseline_fallback"] = zero_baseline_fallback

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        from ..models.alert_anomaly_preview_breach import AlertAnomalyPreviewBreach
        from ..models.alert_anomaly_weekday_preview import AlertAnomalyWeekdayPreview
        from ..models.alert_anomaly_zero_baseline_fallback import AlertAnomalyZeroBaselineFallback

        d = dict(src_dict)
        threshold = d.pop("threshold")

        breaches = []
        _breaches = d.pop("breaches")
        for breaches_item_data in _breaches:
            breaches_item = AlertAnomalyPreviewBreach.from_dict(breaches_item_data)

            breaches.append(breaches_item)

        _weekday_baseline = d.pop("weekday_baseline", UNSET)
        weekday_baseline: AlertAnomalyWeekdayPreview | Unset
        if isinstance(_weekday_baseline, Unset):
            weekday_baseline = UNSET
        else:
            weekday_baseline = AlertAnomalyWeekdayPreview.from_dict(_weekday_baseline)

        _zero_baseline_fallback = d.pop("zero_baseline_fallback", UNSET)
        zero_baseline_fallback: AlertAnomalyZeroBaselineFallback | Unset
        if isinstance(_zero_baseline_fallback, Unset):
            zero_baseline_fallback = UNSET
        else:
            zero_baseline_fallback = AlertAnomalyZeroBaselineFallback.from_dict(_zero_baseline_fallback)

        alert_anomaly_preview_candidate = cls(
            threshold=threshold,
            breaches=breaches,
            weekday_baseline=weekday_baseline,
            zero_baseline_fallback=zero_baseline_fallback,
        )

        alert_anomaly_preview_candidate.additional_properties = d
        return alert_anomaly_preview_candidate

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
