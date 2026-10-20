from typing import Literal

RouteHealthClientErrorFindingStatusCode = Literal[401, 403, 404, 422, 429]

ROUTE_HEALTH_CLIENT_ERROR_FINDING_STATUS_CODE_VALUES: set[RouteHealthClientErrorFindingStatusCode] = {
    401,
    403,
    404,
    422,
    429,
}


def check_route_health_client_error_finding_status_code(value: int) -> RouteHealthClientErrorFindingStatusCode:
    if value in ROUTE_HEALTH_CLIENT_ERROR_FINDING_STATUS_CODE_VALUES:
        return value
    raise TypeError(
        f"Unexpected value {value!r}. Expected one of {ROUTE_HEALTH_CLIENT_ERROR_FINDING_STATUS_CODE_VALUES!r}"
    )
