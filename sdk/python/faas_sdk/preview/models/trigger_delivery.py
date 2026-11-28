from typing import Literal

TriggerDelivery = Literal["batch", "record"]

TRIGGER_DELIVERY_VALUES: set[TriggerDelivery] = {
    "batch",
    "record",
}


def check_trigger_delivery(value: str) -> TriggerDelivery:
    if value in TRIGGER_DELIVERY_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {TRIGGER_DELIVERY_VALUES!r}")
