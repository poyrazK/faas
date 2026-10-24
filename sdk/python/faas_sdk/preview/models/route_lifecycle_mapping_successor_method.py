from typing import Literal

RouteLifecycleMappingSuccessorMethod = Literal["DELETE", "GET", "HEAD", "OPTIONS", "PATCH", "POST", "PUT", "TRACE"]

ROUTE_LIFECYCLE_MAPPING_SUCCESSOR_METHOD_VALUES: set[RouteLifecycleMappingSuccessorMethod] = {
    "DELETE",
    "GET",
    "HEAD",
    "OPTIONS",
    "PATCH",
    "POST",
    "PUT",
    "TRACE",
}


def check_route_lifecycle_mapping_successor_method(value: str) -> RouteLifecycleMappingSuccessorMethod:
    if value in ROUTE_LIFECYCLE_MAPPING_SUCCESSOR_METHOD_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {ROUTE_LIFECYCLE_MAPPING_SUCCESSOR_METHOD_VALUES!r}")
