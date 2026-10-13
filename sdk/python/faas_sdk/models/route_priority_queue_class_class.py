from typing import Literal

RoutePriorityQueueClassClass = Literal["bulk", "critical", "normal"]

ROUTE_PRIORITY_QUEUE_CLASS_CLASS_VALUES: set[RoutePriorityQueueClassClass] = {
    "bulk",
    "critical",
    "normal",
}


def check_route_priority_queue_class_class(value: str) -> RoutePriorityQueueClassClass:
    if value in ROUTE_PRIORITY_QUEUE_CLASS_CLASS_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {ROUTE_PRIORITY_QUEUE_CLASS_CLASS_VALUES!r}")
