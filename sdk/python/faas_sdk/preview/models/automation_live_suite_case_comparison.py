from __future__ import annotations

from collections.abc import Mapping
from typing import TYPE_CHECKING, Any, TypeVar

from attrs import define as _attrs_define

from ..models.automation_live_suite_case_comparison_change import (
    AutomationLiveSuiteCaseComparisonChange,
    check_automation_live_suite_case_comparison_change,
)
from ..types import UNSET, Unset

if TYPE_CHECKING:
    from ..models.automation_live_suite_observation import AutomationLiveSuiteObservation


T = TypeVar("T", bound="AutomationLiveSuiteCaseComparison")


@_attrs_define
class AutomationLiveSuiteCaseComparison:
    """A missing side means the case was not selected in that suite. A selected case without a run has a not_started
    observation.

    """

    name: str
    change: AutomationLiveSuiteCaseComparisonChange
    """Equal hashes and current terminal reports are required for regression, recovery or unchanged. Hash mismatch
    is incompatible even when a report is pending; absent evidence is unknown. Added and removed take precedence
    when only one suite selected the case."""
    baseline: AutomationLiveSuiteObservation | Unset = UNSET
    """Safe original run identity, creation time and recorded result only."""
    candidate: AutomationLiveSuiteObservation | Unset = UNSET
    """Safe original run identity, creation time and recorded result only."""

    def to_dict(self) -> dict[str, Any]:
        name = self.name

        change: str = self.change

        baseline: dict[str, Any] | Unset = UNSET
        if not isinstance(self.baseline, Unset):
            baseline = self.baseline.to_dict()

        candidate: dict[str, Any] | Unset = UNSET
        if not isinstance(self.candidate, Unset):
            candidate = self.candidate.to_dict()

        field_dict: dict[str, Any] = {}

        field_dict.update(
            {
                "name": name,
                "change": change,
            }
        )
        if baseline is not UNSET:
            field_dict["baseline"] = baseline
        if candidate is not UNSET:
            field_dict["candidate"] = candidate

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        from ..models.automation_live_suite_observation import AutomationLiveSuiteObservation

        d = dict(src_dict)
        name = d.pop("name")

        change = check_automation_live_suite_case_comparison_change(d.pop("change"))

        _baseline = d.pop("baseline", UNSET)
        baseline: AutomationLiveSuiteObservation | Unset
        if isinstance(_baseline, Unset):
            baseline = UNSET
        else:
            baseline = AutomationLiveSuiteObservation.from_dict(_baseline)

        _candidate = d.pop("candidate", UNSET)
        candidate: AutomationLiveSuiteObservation | Unset
        if isinstance(_candidate, Unset):
            candidate = UNSET
        else:
            candidate = AutomationLiveSuiteObservation.from_dict(_candidate)

        automation_live_suite_case_comparison = cls(
            name=name,
            change=change,
            baseline=baseline,
            candidate=candidate,
        )

        return automation_live_suite_case_comparison
