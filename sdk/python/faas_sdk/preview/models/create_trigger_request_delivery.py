from typing import Literal

CreateTriggerRequestDelivery = Literal["batch", "record"]

CREATE_TRIGGER_REQUEST_DELIVERY_VALUES: set[CreateTriggerRequestDelivery] = {
    "batch",
    "record",
}


def check_create_trigger_request_delivery(value: str) -> CreateTriggerRequestDelivery:
    if value in CREATE_TRIGGER_REQUEST_DELIVERY_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {CREATE_TRIGGER_REQUEST_DELIVERY_VALUES!r}")
