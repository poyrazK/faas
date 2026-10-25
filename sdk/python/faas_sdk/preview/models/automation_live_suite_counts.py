from __future__ import annotations

from collections.abc import Mapping
from typing import Any, TypeVar

from attrs import define as _attrs_define

T = TypeVar("T", bound="AutomationLiveSuiteCounts")


@_attrs_define
class AutomationLiveSuiteCounts:
    """Current suite case counts, including unstarted and unverifiable members."""

    selected: int
    passed: int
    failed: int
    pending: int
    not_started: int
    unavailable: int
    outdated: int

    def to_dict(self) -> dict[str, Any]:
        selected = self.selected

        passed = self.passed

        failed = self.failed

        pending = self.pending

        not_started = self.not_started

        unavailable = self.unavailable

        outdated = self.outdated

        field_dict: dict[str, Any] = {}

        field_dict.update(
            {
                "selected": selected,
                "passed": passed,
                "failed": failed,
                "pending": pending,
                "not_started": not_started,
                "unavailable": unavailable,
                "outdated": outdated,
            }
        )

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        selected = d.pop("selected")

        passed = d.pop("passed")

        failed = d.pop("failed")

        pending = d.pop("pending")

        not_started = d.pop("not_started")

        unavailable = d.pop("unavailable")

        outdated = d.pop("outdated")

        automation_live_suite_counts = cls(
            selected=selected,
            passed=passed,
            failed=failed,
            pending=pending,
            not_started=not_started,
            unavailable=unavailable,
            outdated=outdated,
        )

        return automation_live_suite_counts
