from __future__ import annotations

import datetime
from collections.abc import Mapping
from typing import TYPE_CHECKING, Any, TypeVar

from attrs import define as _attrs_define

if TYPE_CHECKING:
    from ..models.automation_live_suite_case_comparison import AutomationLiveSuiteCaseComparison
    from ..models.automation_live_suite_comparison_counts import AutomationLiveSuiteComparisonCounts
    from ..models.automation_live_suite_summary import AutomationLiveSuiteSummary


T = TypeVar("T", bound="AutomationLiveSuiteComparison")


@_attrs_define
class AutomationLiveSuiteComparison:
    """Read-only evidence comparison. Outdated identifies target drift, independently of recorded result validity. Contains
    no inputs, outputs, assertions or admission keys.

    """

    baseline: AutomationLiveSuiteSummary
    """Retained suite identity and linked-run counts. Listing is not an assertion verdict."""
    candidate: AutomationLiveSuiteSummary
    """Retained suite identity and linked-run counts. Listing is not an assertion verdict."""
    baseline_outdated: bool
    candidate_outdated: bool
    observed_at: datetime.datetime
    summary: AutomationLiveSuiteComparisonCounts
    """Disjoint case classifications; all categories sum to total. Zero regressions alone is not evidence that the
    candidate passed."""
    cases: list[AutomationLiveSuiteCaseComparison]

    def to_dict(self) -> dict[str, Any]:
        baseline = self.baseline.to_dict()

        candidate = self.candidate.to_dict()

        baseline_outdated = self.baseline_outdated

        candidate_outdated = self.candidate_outdated

        observed_at = self.observed_at.isoformat()

        summary = self.summary.to_dict()

        cases = []
        for cases_item_data in self.cases:
            cases_item = cases_item_data.to_dict()
            cases.append(cases_item)

        field_dict: dict[str, Any] = {}

        field_dict.update(
            {
                "baseline": baseline,
                "candidate": candidate,
                "baseline_outdated": baseline_outdated,
                "candidate_outdated": candidate_outdated,
                "observed_at": observed_at,
                "summary": summary,
                "cases": cases,
            }
        )

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        from ..models.automation_live_suite_case_comparison import AutomationLiveSuiteCaseComparison
        from ..models.automation_live_suite_comparison_counts import AutomationLiveSuiteComparisonCounts
        from ..models.automation_live_suite_summary import AutomationLiveSuiteSummary

        d = dict(src_dict)
        baseline = AutomationLiveSuiteSummary.from_dict(d.pop("baseline"))

        candidate = AutomationLiveSuiteSummary.from_dict(d.pop("candidate"))

        baseline_outdated = d.pop("baseline_outdated")

        candidate_outdated = d.pop("candidate_outdated")

        observed_at = datetime.datetime.fromisoformat(d.pop("observed_at"))

        summary = AutomationLiveSuiteComparisonCounts.from_dict(d.pop("summary"))

        cases = []
        _cases = d.pop("cases")
        for cases_item_data in _cases:
            cases_item = AutomationLiveSuiteCaseComparison.from_dict(cases_item_data)

            cases.append(cases_item)

        automation_live_suite_comparison = cls(
            baseline=baseline,
            candidate=candidate,
            baseline_outdated=baseline_outdated,
            candidate_outdated=candidate_outdated,
            observed_at=observed_at,
            summary=summary,
            cases=cases,
        )

        return automation_live_suite_comparison
