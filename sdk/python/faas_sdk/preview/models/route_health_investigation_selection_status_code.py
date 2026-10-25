from typing import Literal

RouteHealthInvestigationSelectionStatusCode = Literal[0, 401, 403, 404, 422, 429]

ROUTE_HEALTH_INVESTIGATION_SELECTION_STATUS_CODE_VALUES: set[RouteHealthInvestigationSelectionStatusCode] = {
    0,
    401,
    403,
    404,
    422,
    429,
}


def check_route_health_investigation_selection_status_code(value: int) -> RouteHealthInvestigationSelectionStatusCode:
    if value in ROUTE_HEALTH_INVESTIGATION_SELECTION_STATUS_CODE_VALUES:
        return value
    raise TypeError(
        f"Unexpected value {value!r}. Expected one of {ROUTE_HEALTH_INVESTIGATION_SELECTION_STATUS_CODE_VALUES!r}"
    )
