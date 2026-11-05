from typing import Literal

GetRouteHealthInvestigationMethod = Literal["DELETE", "GET", "HEAD", "OPTIONS", "PATCH", "POST", "PUT"]

GET_ROUTE_HEALTH_INVESTIGATION_METHOD_VALUES: set[GetRouteHealthInvestigationMethod] = {
    "DELETE",
    "GET",
    "HEAD",
    "OPTIONS",
    "PATCH",
    "POST",
    "PUT",
}


def check_get_route_health_investigation_method(value: str) -> GetRouteHealthInvestigationMethod:
    if value in GET_ROUTE_HEALTH_INVESTIGATION_METHOD_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {GET_ROUTE_HEALTH_INVESTIGATION_METHOD_VALUES!r}")
