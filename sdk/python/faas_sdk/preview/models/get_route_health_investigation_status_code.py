from typing import Literal

GetRouteHealthInvestigationStatusCode = Literal[0, 401, 403, 404, 422, 429]

GET_ROUTE_HEALTH_INVESTIGATION_STATUS_CODE_VALUES: set[GetRouteHealthInvestigationStatusCode] = {
    0,
    401,
    403,
    404,
    422,
    429,
}


def check_get_route_health_investigation_status_code(value: int) -> GetRouteHealthInvestigationStatusCode:
    if value in GET_ROUTE_HEALTH_INVESTIGATION_STATUS_CODE_VALUES:
        return value
    raise TypeError(
        f"Unexpected value {value!r}. Expected one of {GET_ROUTE_HEALTH_INVESTIGATION_STATUS_CODE_VALUES!r}"
    )
