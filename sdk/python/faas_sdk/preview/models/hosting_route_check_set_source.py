from typing import Literal

HostingRouteCheckSetSource = Literal["app_openapi"]

HOSTING_ROUTE_CHECK_SET_SOURCE_VALUES: set[HostingRouteCheckSetSource] = {
    "app_openapi",
}


def check_hosting_route_check_set_source(value: str) -> HostingRouteCheckSetSource:
    if value in HOSTING_ROUTE_CHECK_SET_SOURCE_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {HOSTING_ROUTE_CHECK_SET_SOURCE_VALUES!r}")
