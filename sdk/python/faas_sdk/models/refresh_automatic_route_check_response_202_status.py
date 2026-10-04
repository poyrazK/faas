from typing import Literal

RefreshAutomaticRouteCheckResponse202Status = Literal["queued"]

REFRESH_AUTOMATIC_ROUTE_CHECK_RESPONSE_202_STATUS_VALUES: set[RefreshAutomaticRouteCheckResponse202Status] = {
    "queued",
}


def check_refresh_automatic_route_check_response_202_status(value: str) -> RefreshAutomaticRouteCheckResponse202Status:
    if value in REFRESH_AUTOMATIC_ROUTE_CHECK_RESPONSE_202_STATUS_VALUES:
        return value
    raise TypeError(
        f"Unexpected value {value!r}. Expected one of {REFRESH_AUTOMATIC_ROUTE_CHECK_RESPONSE_202_STATUS_VALUES!r}"
    )
