from typing import Literal

IssueTrackerProviderStatusProvider = Literal["github", "linear"]

ISSUE_TRACKER_PROVIDER_STATUS_PROVIDER_VALUES: set[IssueTrackerProviderStatusProvider] = {
    "github",
    "linear",
}


def check_issue_tracker_provider_status_provider(value: str) -> IssueTrackerProviderStatusProvider:
    if value in ISSUE_TRACKER_PROVIDER_STATUS_PROVIDER_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {ISSUE_TRACKER_PROVIDER_STATUS_PROVIDER_VALUES!r}")
