from typing import Literal

UpdateTriggerRequestDelivery = Literal["batch", "record"]

UPDATE_TRIGGER_REQUEST_DELIVERY_VALUES: set[UpdateTriggerRequestDelivery] = {
    "batch",
    "record",
}


def check_update_trigger_request_delivery(value: str) -> UpdateTriggerRequestDelivery:
    if value in UPDATE_TRIGGER_REQUEST_DELIVERY_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {UPDATE_TRIGGER_REQUEST_DELIVERY_VALUES!r}")
