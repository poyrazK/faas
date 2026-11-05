from typing import Literal

IssueGroupingExplanationDiscriminatorKind = Literal["error-code", "message", "none", "override"]

ISSUE_GROUPING_EXPLANATION_DISCRIMINATOR_KIND_VALUES: set[IssueGroupingExplanationDiscriminatorKind] = {
    "error-code",
    "message",
    "none",
    "override",
}


def check_issue_grouping_explanation_discriminator_kind(value: str) -> IssueGroupingExplanationDiscriminatorKind:
    if value in ISSUE_GROUPING_EXPLANATION_DISCRIMINATOR_KIND_VALUES:
        return value
    raise TypeError(
        f"Unexpected value {value!r}. Expected one of {ISSUE_GROUPING_EXPLANATION_DISCRIMINATOR_KIND_VALUES!r}"
    )
