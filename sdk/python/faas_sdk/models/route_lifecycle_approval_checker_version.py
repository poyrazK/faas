from typing import Literal

RouteLifecycleApprovalCheckerVersion = Literal[1]

ROUTE_LIFECYCLE_APPROVAL_CHECKER_VERSION_VALUES: set[RouteLifecycleApprovalCheckerVersion] = {
    1,
}


def check_route_lifecycle_approval_checker_version(value: int) -> RouteLifecycleApprovalCheckerVersion:
    if value in ROUTE_LIFECYCLE_APPROVAL_CHECKER_VERSION_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {ROUTE_LIFECYCLE_APPROVAL_CHECKER_VERSION_VALUES!r}")
