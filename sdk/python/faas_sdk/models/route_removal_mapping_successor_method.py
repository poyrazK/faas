from typing import Literal

RouteRemovalMappingSuccessorMethod = Literal["DELETE", "GET", "HEAD", "OPTIONS", "PATCH", "POST", "PUT", "TRACE"]

ROUTE_REMOVAL_MAPPING_SUCCESSOR_METHOD_VALUES: set[RouteRemovalMappingSuccessorMethod] = {
    "DELETE",
    "GET",
    "HEAD",
    "OPTIONS",
    "PATCH",
    "POST",
    "PUT",
    "TRACE",
}


def check_route_removal_mapping_successor_method(value: str) -> RouteRemovalMappingSuccessorMethod:
    if value in ROUTE_REMOVAL_MAPPING_SUCCESSOR_METHOD_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {ROUTE_REMOVAL_MAPPING_SUCCESSOR_METHOD_VALUES!r}")
