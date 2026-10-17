from typing import Literal

EventSubscriptionPendingSummaryCircuitState = Literal["closed", "draining", "half_open", "open"]

EVENT_SUBSCRIPTION_PENDING_SUMMARY_CIRCUIT_STATE_VALUES: set[EventSubscriptionPendingSummaryCircuitState] = {
    "closed",
    "draining",
    "half_open",
    "open",
}


def check_event_subscription_pending_summary_circuit_state(value: str) -> EventSubscriptionPendingSummaryCircuitState:
    if value in EVENT_SUBSCRIPTION_PENDING_SUMMARY_CIRCUIT_STATE_VALUES:
        return value
    raise TypeError(
        f"Unexpected value {value!r}. Expected one of {EVENT_SUBSCRIPTION_PENDING_SUMMARY_CIRCUIT_STATE_VALUES!r}"
    )
