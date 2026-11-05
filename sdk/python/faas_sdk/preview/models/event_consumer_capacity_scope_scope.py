from typing import Literal

EventConsumerCapacityScopeScope = Literal["account", "app", "consumer"]

EVENT_CONSUMER_CAPACITY_SCOPE_SCOPE_VALUES: set[EventConsumerCapacityScopeScope] = {
    "account",
    "app",
    "consumer",
}


def check_event_consumer_capacity_scope_scope(value: str) -> EventConsumerCapacityScopeScope:
    if value in EVENT_CONSUMER_CAPACITY_SCOPE_SCOPE_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {EVENT_CONSUMER_CAPACITY_SCOPE_SCOPE_VALUES!r}")
