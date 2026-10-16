from typing import Literal

GetRouteAwakeScope = Literal["default"]

GET_ROUTE_AWAKE_SCOPE_VALUES: set[GetRouteAwakeScope] = {
    "default",
}


def check_get_route_awake_scope(value: str) -> GetRouteAwakeScope:
    if value in GET_ROUTE_AWAKE_SCOPE_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {GET_ROUTE_AWAKE_SCOPE_VALUES!r}")
