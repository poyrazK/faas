from typing import Literal

AppWebhookDeliveryHealthResponseReceiverState = Literal["awaiting_probe", "cooling_down", "probing", "ready"]

APP_WEBHOOK_DELIVERY_HEALTH_RESPONSE_RECEIVER_STATE_VALUES: set[AppWebhookDeliveryHealthResponseReceiverState] = {
    "awaiting_probe",
    "cooling_down",
    "probing",
    "ready",
}


def check_app_webhook_delivery_health_response_receiver_state(
    value: str,
) -> AppWebhookDeliveryHealthResponseReceiverState:
    if value in APP_WEBHOOK_DELIVERY_HEALTH_RESPONSE_RECEIVER_STATE_VALUES:
        return value
    raise TypeError(
        f"Unexpected value {value!r}. Expected one of {APP_WEBHOOK_DELIVERY_HEALTH_RESPONSE_RECEIVER_STATE_VALUES!r}"
    )
