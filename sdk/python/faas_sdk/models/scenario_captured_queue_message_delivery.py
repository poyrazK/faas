from typing import Literal

ScenarioCapturedQueueMessageDelivery = Literal["delivered", "overflow", "unsupported"]

SCENARIO_CAPTURED_QUEUE_MESSAGE_DELIVERY_VALUES: set[ScenarioCapturedQueueMessageDelivery] = {
    "delivered",
    "overflow",
    "unsupported",
}


def check_scenario_captured_queue_message_delivery(value: str) -> ScenarioCapturedQueueMessageDelivery:
    if value in SCENARIO_CAPTURED_QUEUE_MESSAGE_DELIVERY_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {SCENARIO_CAPTURED_QUEUE_MESSAGE_DELIVERY_VALUES!r}")
