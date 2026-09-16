from __future__ import annotations

from collections.abc import Mapping
from typing import Any, TypeVar

from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..models.plan_detection_warning_detector import PlanDetectionWarningDetector, check_plan_detection_warning_detector
from ..models.plan_detection_warning_outcome import PlanDetectionWarningOutcome, check_plan_detection_warning_outcome
from ..types import UNSET, Unset

T = TypeVar("T", bound="PlanDetectionWarning")


@_attrs_define
class PlanDetectionWarning:
    """One detector decision that did not become a standalone workload
    (issue #742). Outcome is merged when the candidate collapsed into
    the winning workload, or skipped when the detector rejected it.
    The legacy warnings string list remains available for compatibility.

    """

    detector: PlanDetectionWarningDetector
    marker: str
    """Concrete source marker associated with the detector decision."""
    priority: int
    """Detector priority used by the merge tiebreak."""
    outcome: PlanDetectionWarningOutcome
    reason: str
    """Stable human-readable reason for the detector decision."""
    workload: str | Unset = UNSET
    """Workload affected by the decision; omitted for source-wide warnings."""
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        detector: str = self.detector

        marker = self.marker

        priority = self.priority

        outcome: str = self.outcome

        reason = self.reason

        workload = self.workload

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "detector": detector,
                "marker": marker,
                "priority": priority,
                "outcome": outcome,
                "reason": reason,
            }
        )
        if workload is not UNSET:
            field_dict["workload"] = workload

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        detector = check_plan_detection_warning_detector(d.pop("detector"))

        marker = d.pop("marker")

        priority = d.pop("priority")

        outcome = check_plan_detection_warning_outcome(d.pop("outcome"))

        reason = d.pop("reason")

        workload = d.pop("workload", UNSET)

        plan_detection_warning = cls(
            detector=detector,
            marker=marker,
            priority=priority,
            outcome=outcome,
            reason=reason,
            workload=workload,
        )

        plan_detection_warning.additional_properties = d
        return plan_detection_warning

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
