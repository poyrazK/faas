from __future__ import annotations

from collections.abc import Mapping
from typing import TYPE_CHECKING, Any, TypeVar

from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..types import UNSET, Unset

if TYPE_CHECKING:
    from ..models.debug_critical_path_exemplar import DebugCriticalPathExemplar
    from ..models.debug_critical_path_segment import DebugCriticalPathSegment


T = TypeVar("T", bound="DebugCriticalPathHistoryItem")


@_attrs_define
class DebugCriticalPathHistoryItem:
    """Bounded historical critical-path aggregate. Percentiles are weighted by collapsed request-row counts and derived
    from sampled redacted spans.

    """

    signature: str
    segments: list[DebugCriticalPathSegment]
    exemplars: list[DebugCriticalPathExemplar]
    calls: int
    error_calls: int
    error_rate_pct: float
    p50_ms: int
    p95_ms: int
    p99_ms: int
    baseline_calls: int
    current_calls: int
    baseline_p95_ms: int
    current_p95_ms: int
    p95_delta_ms: int
    regression_factor: float
    regression: bool
    """True when both path samples meet the minimum threshold and the newer p95 exceeds the older p95 by the
    configured factor and delta."""
    baseline_error_rate_pct: float
    current_error_rate_pct: float
    error_rate_delta_pct: float
    dominant_segment: DebugCriticalPathSegment | Unset = UNSET
    """One redacted span identity in a canonical historical critical-path signature."""
    dominant_segment_exclusive_ms: int | Unset = UNSET
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        signature = self.signature

        segments = []
        for segments_item_data in self.segments:
            segments_item = segments_item_data.to_dict()
            segments.append(segments_item)

        exemplars = []
        for exemplars_item_data in self.exemplars:
            exemplars_item = exemplars_item_data.to_dict()
            exemplars.append(exemplars_item)

        calls = self.calls

        error_calls = self.error_calls

        error_rate_pct = self.error_rate_pct

        p50_ms = self.p50_ms

        p95_ms = self.p95_ms

        p99_ms = self.p99_ms

        baseline_calls = self.baseline_calls

        current_calls = self.current_calls

        baseline_p95_ms = self.baseline_p95_ms

        current_p95_ms = self.current_p95_ms

        p95_delta_ms = self.p95_delta_ms

        regression_factor = self.regression_factor

        regression = self.regression

        baseline_error_rate_pct = self.baseline_error_rate_pct

        current_error_rate_pct = self.current_error_rate_pct

        error_rate_delta_pct = self.error_rate_delta_pct

        dominant_segment: dict[str, Any] | Unset = UNSET
        if not isinstance(self.dominant_segment, Unset):
            dominant_segment = self.dominant_segment.to_dict()

        dominant_segment_exclusive_ms = self.dominant_segment_exclusive_ms

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "signature": signature,
                "segments": segments,
                "exemplars": exemplars,
                "calls": calls,
                "error_calls": error_calls,
                "error_rate_pct": error_rate_pct,
                "p50_ms": p50_ms,
                "p95_ms": p95_ms,
                "p99_ms": p99_ms,
                "baseline_calls": baseline_calls,
                "current_calls": current_calls,
                "baseline_p95_ms": baseline_p95_ms,
                "current_p95_ms": current_p95_ms,
                "p95_delta_ms": p95_delta_ms,
                "regression_factor": regression_factor,
                "regression": regression,
                "baseline_error_rate_pct": baseline_error_rate_pct,
                "current_error_rate_pct": current_error_rate_pct,
                "error_rate_delta_pct": error_rate_delta_pct,
            }
        )
        if dominant_segment is not UNSET:
            field_dict["dominant_segment"] = dominant_segment
        if dominant_segment_exclusive_ms is not UNSET:
            field_dict["dominant_segment_exclusive_ms"] = dominant_segment_exclusive_ms

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        from ..models.debug_critical_path_exemplar import DebugCriticalPathExemplar
        from ..models.debug_critical_path_segment import DebugCriticalPathSegment

        d = dict(src_dict)
        signature = d.pop("signature")

        segments = []
        _segments = d.pop("segments")
        for segments_item_data in _segments:
            segments_item = DebugCriticalPathSegment.from_dict(segments_item_data)

            segments.append(segments_item)

        exemplars = []
        _exemplars = d.pop("exemplars")
        for exemplars_item_data in _exemplars:
            exemplars_item = DebugCriticalPathExemplar.from_dict(exemplars_item_data)

            exemplars.append(exemplars_item)

        calls = d.pop("calls")

        error_calls = d.pop("error_calls")

        error_rate_pct = d.pop("error_rate_pct")

        p50_ms = d.pop("p50_ms")

        p95_ms = d.pop("p95_ms")

        p99_ms = d.pop("p99_ms")

        baseline_calls = d.pop("baseline_calls")

        current_calls = d.pop("current_calls")

        baseline_p95_ms = d.pop("baseline_p95_ms")

        current_p95_ms = d.pop("current_p95_ms")

        p95_delta_ms = d.pop("p95_delta_ms")

        regression_factor = d.pop("regression_factor")

        regression = d.pop("regression")

        baseline_error_rate_pct = d.pop("baseline_error_rate_pct")

        current_error_rate_pct = d.pop("current_error_rate_pct")

        error_rate_delta_pct = d.pop("error_rate_delta_pct")

        _dominant_segment = d.pop("dominant_segment", UNSET)
        dominant_segment: DebugCriticalPathSegment | Unset
        if isinstance(_dominant_segment, Unset):
            dominant_segment = UNSET
        else:
            dominant_segment = DebugCriticalPathSegment.from_dict(_dominant_segment)

        dominant_segment_exclusive_ms = d.pop("dominant_segment_exclusive_ms", UNSET)

        debug_critical_path_history_item = cls(
            signature=signature,
            segments=segments,
            exemplars=exemplars,
            calls=calls,
            error_calls=error_calls,
            error_rate_pct=error_rate_pct,
            p50_ms=p50_ms,
            p95_ms=p95_ms,
            p99_ms=p99_ms,
            baseline_calls=baseline_calls,
            current_calls=current_calls,
            baseline_p95_ms=baseline_p95_ms,
            current_p95_ms=current_p95_ms,
            p95_delta_ms=p95_delta_ms,
            regression_factor=regression_factor,
            regression=regression,
            baseline_error_rate_pct=baseline_error_rate_pct,
            current_error_rate_pct=current_error_rate_pct,
            error_rate_delta_pct=error_rate_delta_pct,
            dominant_segment=dominant_segment,
            dominant_segment_exclusive_ms=dominant_segment_exclusive_ms,
        )

        debug_critical_path_history_item.additional_properties = d
        return debug_critical_path_history_item

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
