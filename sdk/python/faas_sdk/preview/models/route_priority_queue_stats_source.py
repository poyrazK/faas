from typing import Literal

RoutePriorityQueueStatsSource = Literal["live", "unavailable"]

ROUTE_PRIORITY_QUEUE_STATS_SOURCE_VALUES: set[RoutePriorityQueueStatsSource] = {
    "live",
    "unavailable",
}


def check_route_priority_queue_stats_source(value: str) -> RoutePriorityQueueStatsSource:
    if value in ROUTE_PRIORITY_QUEUE_STATS_SOURCE_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {ROUTE_PRIORITY_QUEUE_STATS_SOURCE_VALUES!r}")
