from typing import Literal

GetRouteAdviceScope = Literal["default"]

GET_ROUTE_ADVICE_SCOPE_VALUES: set[GetRouteAdviceScope] = {
    "default",
}


def check_get_route_advice_scope(value: str) -> GetRouteAdviceScope:
    if value in GET_ROUTE_ADVICE_SCOPE_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {GET_ROUTE_ADVICE_SCOPE_VALUES!r}")
