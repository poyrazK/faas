from typing import Literal

IssueSplitPreviewApplicationStatus = Literal["derived-branch", "existing-rule", "new-rule", "rule-limit"]

ISSUE_SPLIT_PREVIEW_APPLICATION_STATUS_VALUES: set[IssueSplitPreviewApplicationStatus] = {
    "derived-branch",
    "existing-rule",
    "new-rule",
    "rule-limit",
}


def check_issue_split_preview_application_status(value: str) -> IssueSplitPreviewApplicationStatus:
    if value in ISSUE_SPLIT_PREVIEW_APPLICATION_STATUS_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {ISSUE_SPLIT_PREVIEW_APPLICATION_STATUS_VALUES!r}")
