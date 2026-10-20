from typing import Literal

RouteRemovalMappingMethod = Literal["DELETE", "GET", "HEAD", "OPTIONS", "PATCH", "POST", "PUT", "TRACE"]

ROUTE_REMOVAL_MAPPING_METHOD_VALUES: set[RouteRemovalMappingMethod] = {
    "DELETE",
    "GET",
    "HEAD",
    "OPTIONS",
    "PATCH",
    "POST",
    "PUT",
    "TRACE",
}


def check_route_removal_mapping_method(value: str) -> RouteRemovalMappingMethod:
    if value in ROUTE_REMOVAL_MAPPING_METHOD_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {ROUTE_REMOVAL_MAPPING_METHOD_VALUES!r}")
