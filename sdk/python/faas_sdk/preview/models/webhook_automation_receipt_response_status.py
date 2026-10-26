from typing import Literal

WebhookAutomationReceiptResponseStatus = Literal["accepted", "ignored"]

WEBHOOK_AUTOMATION_RECEIPT_RESPONSE_STATUS_VALUES: set[WebhookAutomationReceiptResponseStatus] = {
    "accepted",
    "ignored",
}


def check_webhook_automation_receipt_response_status(value: str) -> WebhookAutomationReceiptResponseStatus:
    if value in WEBHOOK_AUTOMATION_RECEIPT_RESPONSE_STATUS_VALUES:
        return value
    raise TypeError(
        f"Unexpected value {value!r}. Expected one of {WEBHOOK_AUTOMATION_RECEIPT_RESPONSE_STATUS_VALUES!r}"
    )
