from __future__ import annotations

from collections.abc import Mapping
from typing import Any, TypeVar

from attrs import define as _attrs_define

T = TypeVar("T", bound="AutomationLiveSuiteComparisonCounts")


@_attrs_define
class AutomationLiveSuiteComparisonCounts:
    """Disjoint case classifications; all categories sum to total. Zero regressions alone is not evidence that the
    candidate passed.

    """

    total: int
    regressions: int
    recoveries: int
    unchanged: int
    added: int
    removed: int
    incompatible: int
    unknown: int

    def to_dict(self) -> dict[str, Any]:
        total = self.total

        regressions = self.regressions

        recoveries = self.recoveries

        unchanged = self.unchanged

        added = self.added

        removed = self.removed

        incompatible = self.incompatible

        unknown = self.unknown

        field_dict: dict[str, Any] = {}

        field_dict.update(
            {
                "total": total,
                "regressions": regressions,
                "recoveries": recoveries,
                "unchanged": unchanged,
                "added": added,
                "removed": removed,
                "incompatible": incompatible,
                "unknown": unknown,
            }
        )

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        total = d.pop("total")

        regressions = d.pop("regressions")

        recoveries = d.pop("recoveries")

        unchanged = d.pop("unchanged")

        added = d.pop("added")

        removed = d.pop("removed")

        incompatible = d.pop("incompatible")

        unknown = d.pop("unknown")

        automation_live_suite_comparison_counts = cls(
            total=total,
            regressions=regressions,
            recoveries=recoveries,
            unchanged=unchanged,
            added=added,
            removed=removed,
            incompatible=incompatible,
            unknown=unknown,
        )

        return automation_live_suite_comparison_counts
