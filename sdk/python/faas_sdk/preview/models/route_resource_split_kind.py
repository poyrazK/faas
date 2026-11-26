from typing import Literal

RouteResourceSplitKind = Literal["cpu", "memory"]

ROUTE_RESOURCE_SPLIT_KIND_VALUES: set[RouteResourceSplitKind] = {
    "cpu",
    "memory",
}


def check_route_resource_split_kind(value: str) -> RouteResourceSplitKind:
    if value in ROUTE_RESOURCE_SPLIT_KIND_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {ROUTE_RESOURCE_SPLIT_KIND_VALUES!r}")
