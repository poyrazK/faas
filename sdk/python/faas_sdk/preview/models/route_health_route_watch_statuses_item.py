from typing import Literal

RouteHealthRouteWatchStatusesItem = Literal[401, 403, 404, 422, 429]

ROUTE_HEALTH_ROUTE_WATCH_STATUSES_ITEM_VALUES: set[RouteHealthRouteWatchStatusesItem] = {
    401,
    403,
    404,
    422,
    429,
}


def check_route_health_route_watch_statuses_item(value: int) -> RouteHealthRouteWatchStatusesItem:
    if value in ROUTE_HEALTH_ROUTE_WATCH_STATUSES_ITEM_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {ROUTE_HEALTH_ROUTE_WATCH_STATUSES_ITEM_VALUES!r}")
