from typing import Literal

IssueReleaseHealthRequestCoverage = Literal["observed", "unknown"]

ISSUE_RELEASE_HEALTH_REQUEST_COVERAGE_VALUES: set[IssueReleaseHealthRequestCoverage] = {
    "observed",
    "unknown",
}


def check_issue_release_health_request_coverage(value: str) -> IssueReleaseHealthRequestCoverage:
    if value in ISSUE_RELEASE_HEALTH_REQUEST_COVERAGE_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {ISSUE_RELEASE_HEALTH_REQUEST_COVERAGE_VALUES!r}")
