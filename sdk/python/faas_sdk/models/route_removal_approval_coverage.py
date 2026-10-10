from typing import Literal

RouteRemovalApprovalCoverage = Literal["observed_only"]

ROUTE_REMOVAL_APPROVAL_COVERAGE_VALUES: set[RouteRemovalApprovalCoverage] = {
    "observed_only",
}


def check_route_removal_approval_coverage(value: str) -> RouteRemovalApprovalCoverage:
    if value in ROUTE_REMOVAL_APPROVAL_COVERAGE_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {ROUTE_REMOVAL_APPROVAL_COVERAGE_VALUES!r}")
