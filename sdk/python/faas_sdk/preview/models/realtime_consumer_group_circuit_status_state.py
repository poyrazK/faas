from typing import Literal

RealtimeConsumerGroupCircuitStatusState = Literal["closed", "half_open", "open"]

REALTIME_CONSUMER_GROUP_CIRCUIT_STATUS_STATE_VALUES: set[RealtimeConsumerGroupCircuitStatusState] = {
    "closed",
    "half_open",
    "open",
}


def check_realtime_consumer_group_circuit_status_state(value: str) -> RealtimeConsumerGroupCircuitStatusState:
    if value in REALTIME_CONSUMER_GROUP_CIRCUIT_STATUS_STATE_VALUES:
        return value
    raise TypeError(
        f"Unexpected value {value!r}. Expected one of {REALTIME_CONSUMER_GROUP_CIRCUIT_STATUS_STATE_VALUES!r}"
    )
