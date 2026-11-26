from typing import Literal

RouteSplitKind = Literal["cpu", "memory"]

ROUTE_SPLIT_KIND_VALUES: set[RouteSplitKind] = {
    "cpu",
    "memory",
}


def check_route_split_kind(value: str) -> RouteSplitKind:
    if value in ROUTE_SPLIT_KIND_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {ROUTE_SPLIT_KIND_VALUES!r}")
