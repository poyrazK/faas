from typing import Literal

EventConsumerCapacityPressureBlockingScope = Literal["", "account", "app", "consumer"]

EVENT_CONSUMER_CAPACITY_PRESSURE_BLOCKING_SCOPE_VALUES: set[EventConsumerCapacityPressureBlockingScope] = {
    "",
    "account",
    "app",
    "consumer",
}


def check_event_consumer_capacity_pressure_blocking_scope(value: str) -> EventConsumerCapacityPressureBlockingScope:
    if value in EVENT_CONSUMER_CAPACITY_PRESSURE_BLOCKING_SCOPE_VALUES:
        return value
    raise TypeError(
        f"Unexpected value {value!r}. Expected one of {EVENT_CONSUMER_CAPACITY_PRESSURE_BLOCKING_SCOPE_VALUES!r}"
    )
