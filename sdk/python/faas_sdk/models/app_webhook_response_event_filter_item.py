from typing import Literal

AppWebhookResponseEventFilterItem = Literal[
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
    "usage_statement.finalized",
]

APP_WEBHOOK_RESPONSE_EVENT_FILTER_ITEM_VALUES: set[AppWebhookResponseEventFilterItem] = {
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
    "usage_statement.finalized",
}


def check_app_webhook_response_event_filter_item(value: str) -> AppWebhookResponseEventFilterItem:
    if value in APP_WEBHOOK_RESPONSE_EVENT_FILTER_ITEM_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {APP_WEBHOOK_RESPONSE_EVENT_FILTER_ITEM_VALUES!r}")
