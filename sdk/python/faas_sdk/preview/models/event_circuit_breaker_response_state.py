from typing import Literal

EventCircuitBreakerResponseState = Literal["closed", "disabled", "draining", "half_open", "open"]

EVENT_CIRCUIT_BREAKER_RESPONSE_STATE_VALUES: set[EventCircuitBreakerResponseState] = {
    "closed",
    "disabled",
    "draining",
    "half_open",
    "open",
}


def check_event_circuit_breaker_response_state(value: str) -> EventCircuitBreakerResponseState:
    if value in EVENT_CIRCUIT_BREAKER_RESPONSE_STATE_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {EVENT_CIRCUIT_BREAKER_RESPONSE_STATE_VALUES!r}")
