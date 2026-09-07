from typing import Literal

CreateAppWebhookRequestEventFilterItem = Literal[
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

CREATE_APP_WEBHOOK_REQUEST_EVENT_FILTER_ITEM_VALUES: set[CreateAppWebhookRequestEventFilterItem] = {
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


def check_create_app_webhook_request_event_filter_item(value: str) -> CreateAppWebhookRequestEventFilterItem:
    if value in CREATE_APP_WEBHOOK_REQUEST_EVENT_FILTER_ITEM_VALUES:
        return value
    raise TypeError(
        f"Unexpected value {value!r}. Expected one of {CREATE_APP_WEBHOOK_REQUEST_EVENT_FILTER_ITEM_VALUES!r}"
    )
