from typing import Literal

RouteLifecycleApprovalCompatibility = Literal["no_supported_breaks"]

ROUTE_LIFECYCLE_APPROVAL_COMPATIBILITY_VALUES: set[RouteLifecycleApprovalCompatibility] = {
    "no_supported_breaks",
}


def check_route_lifecycle_approval_compatibility(value: str) -> RouteLifecycleApprovalCompatibility:
    if value in ROUTE_LIFECYCLE_APPROVAL_COMPATIBILITY_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {ROUTE_LIFECYCLE_APPROVAL_COMPATIBILITY_VALUES!r}")
