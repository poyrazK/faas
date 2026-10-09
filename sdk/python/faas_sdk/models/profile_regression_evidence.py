from __future__ import annotations

from collections.abc import Mapping
from typing import TYPE_CHECKING, Any, TypeVar

from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..models.profile_regression_evidence_kind import (
    ProfileRegressionEvidenceKind,
    check_profile_regression_evidence_kind,
)

if TYPE_CHECKING:
    from ..models.profile_call_path_frame import ProfileCallPathFrame
    from ..models.profile_regression_metric import ProfileRegressionMetric


T = TypeVar("T", bound="ProfileRegressionEvidence")


@_attrs_define
class ProfileRegressionEvidence:
    """Comparable function self CPU or complete caller-path inclusive CPU meeting both thresholds. Inclusive path entries
    overlap and must not be summed. Frame names and source paths are observed profile symbols; use an authorized profile
    comparison to inspect source when available.

    """

    kind: ProfileRegressionEvidenceKind
    frames: list[ProfileCallPathFrame]
    metric: ProfileRegressionMetric
    """Observed sampled CPU/s comparison, optionally with CPU seconds per weighted observed request.
    exceeds_threshold and relative_increase_percent use the metric selected by options. The relative percentage is
    absent when that metric's baseline is zero."""
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        kind: str = self.kind

        frames = []
        for frames_item_data in self.frames:
            frames_item = frames_item_data.to_dict()
            frames.append(frames_item)

        metric = self.metric.to_dict()

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "kind": kind,
                "frames": frames,
                "metric": metric,
            }
        )

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        from ..models.profile_call_path_frame import ProfileCallPathFrame
        from ..models.profile_regression_metric import ProfileRegressionMetric

        d = dict(src_dict)
        kind = check_profile_regression_evidence_kind(d.pop("kind"))

        frames = []
        _frames = d.pop("frames")
        for frames_item_data in _frames:
            frames_item = ProfileCallPathFrame.from_dict(frames_item_data)

            frames.append(frames_item)

        metric = ProfileRegressionMetric.from_dict(d.pop("metric"))

        profile_regression_evidence = cls(
            kind=kind,
            frames=frames,
            metric=metric,
        )

        profile_regression_evidence.additional_properties = d
        return profile_regression_evidence

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
