from typing import Literal

AppWebhookDeliveryAttemptResponseOutcome = Literal["dead", "retrying", "succeeded"]

APP_WEBHOOK_DELIVERY_ATTEMPT_RESPONSE_OUTCOME_VALUES: set[AppWebhookDeliveryAttemptResponseOutcome] = {
    "dead",
    "retrying",
    "succeeded",
}


def check_app_webhook_delivery_attempt_response_outcome(value: str) -> AppWebhookDeliveryAttemptResponseOutcome:
    if value in APP_WEBHOOK_DELIVERY_ATTEMPT_RESPONSE_OUTCOME_VALUES:
        return value
    raise TypeError(
        f"Unexpected value {value!r}. Expected one of {APP_WEBHOOK_DELIVERY_ATTEMPT_RESPONSE_OUTCOME_VALUES!r}"
    )
