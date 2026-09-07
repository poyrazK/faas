from typing import Literal

AppWebhookDeliveryResponseEvent = Literal[
    "app.created",
    "app.deleted",
    "app.deployed",
    "app.parked",
    "app.scaled",
    "app.woken",
    "budget.threshold",
    "build.failed",
    "build.succeeded",
    "cron.fired",
    "cron.fired.manually",
    "deployment.failed",
    "error.new",
    "job.finished",
    "preview.created",
    "rollout.aborted",
]

APP_WEBHOOK_DELIVERY_RESPONSE_EVENT_VALUES: set[AppWebhookDeliveryResponseEvent] = {
    "app.created",
    "app.deleted",
    "app.deployed",
    "app.parked",
    "app.scaled",
    "app.woken",
    "budget.threshold",
    "build.failed",
    "build.succeeded",
    "cron.fired",
    "cron.fired.manually",
    "deployment.failed",
    "error.new",
    "job.finished",
    "preview.created",
    "rollout.aborted",
}


def check_app_webhook_delivery_response_event(value: str) -> AppWebhookDeliveryResponseEvent:
    if value in APP_WEBHOOK_DELIVERY_RESPONSE_EVENT_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {APP_WEBHOOK_DELIVERY_RESPONSE_EVENT_VALUES!r}")
