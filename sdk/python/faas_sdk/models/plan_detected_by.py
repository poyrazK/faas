from __future__ import annotations

from collections.abc import Mapping
from typing import Any, TypeVar

from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..models.plan_detected_by_detector import PlanDetectedByDetector, check_plan_detected_by_detector
from ..models.plan_detected_by_merged_from_item import (
    PlanDetectedByMergedFromItem,
    check_plan_detected_by_merged_from_item,
)
from ..types import UNSET, Unset

T = TypeVar("T", bound="PlanDetectedBy")


@_attrs_define
class PlanDetectedBy:
    """Structured detection trace for one workload (issue #742).
    `source` on PlanWorkload carries the same provenance as free
    text ("compose.yaml: api"); this is the machine-readable form
    so a client can branch on the detector without parsing it.

    Additive and optional: absent on any response the server did
    not populate, so existing consumers are unaffected.

    """

    detector: PlanDetectedByDetector
    """closed vocabulary — the detector that won identity for this workload"""
    priority: int
    """The detector's tiebreak weight; higher wins identity
    within a tier. Surfaced so the precedence order is visible
    on the wire rather than implied by server source.
    """
    merged_from: list[PlanDetectedByMergedFromItem] | Unset = UNSET
    """Other detectors whose seeds collapsed into this workload
    under the (root_dir, name) merge key, deduplicated and
    deterministically ordered. Absent when the workload came
    from a single seed. Answers "why didn't my Procfile `web`
    get its own workload?" — it merged into the compose `web`.
    """
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        detector: str = self.detector

        priority = self.priority

        merged_from: list[str] | Unset = UNSET
        if not isinstance(self.merged_from, Unset):
            merged_from = []
            for merged_from_item_data in self.merged_from:
                merged_from_item: str = merged_from_item_data
                merged_from.append(merged_from_item)

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "detector": detector,
                "priority": priority,
            }
        )
        if merged_from is not UNSET:
            field_dict["merged_from"] = merged_from

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        detector = check_plan_detected_by_detector(d.pop("detector"))

        priority = d.pop("priority")

        _merged_from = d.pop("merged_from", UNSET)
        merged_from: list[PlanDetectedByMergedFromItem] | Unset = UNSET
        if _merged_from is not UNSET:
            merged_from = []
            for merged_from_item_data in _merged_from:
                merged_from_item = check_plan_detected_by_merged_from_item(merged_from_item_data)

                merged_from.append(merged_from_item)

        plan_detected_by = cls(
            detector=detector,
            priority=priority,
            merged_from=merged_from,
        )

        plan_detected_by.additional_properties = d
        return plan_detected_by

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
