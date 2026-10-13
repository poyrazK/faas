from typing import Literal

IssueGroupingExplanationBasis = Literal["application-frames", "failure-site", "fallback", "override"]

ISSUE_GROUPING_EXPLANATION_BASIS_VALUES: set[IssueGroupingExplanationBasis] = {
    "application-frames",
    "failure-site",
    "fallback",
    "override",
}


def check_issue_grouping_explanation_basis(value: str) -> IssueGroupingExplanationBasis:
    if value in ISSUE_GROUPING_EXPLANATION_BASIS_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {ISSUE_GROUPING_EXPLANATION_BASIS_VALUES!r}")
