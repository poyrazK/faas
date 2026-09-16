from typing import Literal

AppWebhookResponseEventFilterItem = Literal["app.parked", "app.woken", "usage_statement.finalized"]

APP_WEBHOOK_RESPONSE_EVENT_FILTER_ITEM_VALUES: set[AppWebhookResponseEventFilterItem] = {
    "app.parked",
    "app.woken",
    "usage_statement.finalized",
}


def check_app_webhook_response_event_filter_item(value: str) -> AppWebhookResponseEventFilterItem:
    if value in APP_WEBHOOK_RESPONSE_EVENT_FILTER_ITEM_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {APP_WEBHOOK_RESPONSE_EVENT_FILTER_ITEM_VALUES!r}")
