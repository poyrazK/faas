from typing import Literal

TriggerRoutedTo = Literal["customer_dlq", "drop", "manual_retry"]

TRIGGER_ROUTED_TO_VALUES: set[TriggerRoutedTo] = {
    "customer_dlq",
    "drop",
    "manual_retry",
}


def check_trigger_routed_to(value: str) -> TriggerRoutedTo:
    if value in TRIGGER_ROUTED_TO_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {TRIGGER_ROUTED_TO_VALUES!r}")
