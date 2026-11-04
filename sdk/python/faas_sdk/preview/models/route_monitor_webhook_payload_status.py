from typing import Literal

RouteMonitorWebhookPayloadStatus = Literal["open", "recovered"]

ROUTE_MONITOR_WEBHOOK_PAYLOAD_STATUS_VALUES: set[RouteMonitorWebhookPayloadStatus] = {
    "open",
    "recovered",
}


def check_route_monitor_webhook_payload_status(value: str) -> RouteMonitorWebhookPayloadStatus:
    if value in ROUTE_MONITOR_WEBHOOK_PAYLOAD_STATUS_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {ROUTE_MONITOR_WEBHOOK_PAYLOAD_STATUS_VALUES!r}")
