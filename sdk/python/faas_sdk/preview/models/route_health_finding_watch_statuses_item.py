from typing import Literal

RouteHealthFindingWatchStatusesItem = Literal[401, 403, 404, 422, 429]

ROUTE_HEALTH_FINDING_WATCH_STATUSES_ITEM_VALUES: set[RouteHealthFindingWatchStatusesItem] = {
    401,
    403,
    404,
    422,
    429,
}


def check_route_health_finding_watch_statuses_item(value: int) -> RouteHealthFindingWatchStatusesItem:
    if value in ROUTE_HEALTH_FINDING_WATCH_STATUSES_ITEM_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {ROUTE_HEALTH_FINDING_WATCH_STATUSES_ITEM_VALUES!r}")
