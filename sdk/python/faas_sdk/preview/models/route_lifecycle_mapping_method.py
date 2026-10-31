from typing import Literal

RouteLifecycleMappingMethod = Literal["DELETE", "GET", "HEAD", "OPTIONS", "PATCH", "POST", "PUT", "TRACE"]

ROUTE_LIFECYCLE_MAPPING_METHOD_VALUES: set[RouteLifecycleMappingMethod] = {
    "DELETE",
    "GET",
    "HEAD",
    "OPTIONS",
    "PATCH",
    "POST",
    "PUT",
    "TRACE",
}


def check_route_lifecycle_mapping_method(value: str) -> RouteLifecycleMappingMethod:
    if value in ROUTE_LIFECYCLE_MAPPING_METHOD_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {ROUTE_LIFECYCLE_MAPPING_METHOD_VALUES!r}")
