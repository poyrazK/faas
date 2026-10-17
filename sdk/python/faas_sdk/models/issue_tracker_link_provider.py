from typing import Literal

IssueTrackerLinkProvider = Literal["github", "linear"]

ISSUE_TRACKER_LINK_PROVIDER_VALUES: set[IssueTrackerLinkProvider] = {
    "github",
    "linear",
}


def check_issue_tracker_link_provider(value: str) -> IssueTrackerLinkProvider:
    if value in ISSUE_TRACKER_LINK_PROVIDER_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {ISSUE_TRACKER_LINK_PROVIDER_VALUES!r}")
