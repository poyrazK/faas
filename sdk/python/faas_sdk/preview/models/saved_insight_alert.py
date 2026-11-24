from __future__ import annotations

from collections.abc import Mapping
from typing import Any, TypeVar

from attrs import define as _attrs_define

from ..models.saved_insight_alert_comparison import SavedInsightAlertComparison, check_saved_insight_alert_comparison

T = TypeVar("T", bound="SavedInsightAlert")


@_attrs_define
class SavedInsightAlert:
    """Fires when the saved insight's value compares to threshold; funnel and retention values are fractions."""

    comparison: SavedInsightAlertComparison
    threshold: float

    def to_dict(self) -> dict[str, Any]:
        comparison: str = self.comparison

        threshold = self.threshold

        field_dict: dict[str, Any] = {}

        field_dict.update(
            {
                "comparison": comparison,
                "threshold": threshold,
            }
        )

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        comparison = check_saved_insight_alert_comparison(d.pop("comparison"))

        threshold = d.pop("threshold")

        saved_insight_alert = cls(
            comparison=comparison,
            threshold=threshold,
        )

        return saved_insight_alert
