from typing import Literal

EventConsumerCapacityPressureCoverage = Literal["live_slots_and_all_pending_application_recipients"]

EVENT_CONSUMER_CAPACITY_PRESSURE_COVERAGE_VALUES: set[EventConsumerCapacityPressureCoverage] = {
    "live_slots_and_all_pending_application_recipients",
}


def check_event_consumer_capacity_pressure_coverage(value: str) -> EventConsumerCapacityPressureCoverage:
    if value in EVENT_CONSUMER_CAPACITY_PRESSURE_COVERAGE_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {EVENT_CONSUMER_CAPACITY_PRESSURE_COVERAGE_VALUES!r}")
