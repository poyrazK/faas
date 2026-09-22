from typing import Literal

InboundWebhookReceiptResponseStatus = Literal["accepted"]

INBOUND_WEBHOOK_RECEIPT_RESPONSE_STATUS_VALUES: set[InboundWebhookReceiptResponseStatus] = {
    "accepted",
}


def check_inbound_webhook_receipt_response_status(value: str) -> InboundWebhookReceiptResponseStatus:
    if value in INBOUND_WEBHOOK_RECEIPT_RESPONSE_STATUS_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {INBOUND_WEBHOOK_RECEIPT_RESPONSE_STATUS_VALUES!r}")
