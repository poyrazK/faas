from typing import Literal

WebhookAutomationReceiptResponseIgnoredReason = Literal[
    "automation_failure_paused", "automation_paused", "automation_unpublished", "event_filtered"
]

WEBHOOK_AUTOMATION_RECEIPT_RESPONSE_IGNORED_REASON_VALUES: set[WebhookAutomationReceiptResponseIgnoredReason] = {
    "automation_failure_paused",
    "automation_paused",
    "automation_unpublished",
    "event_filtered",
}


def check_webhook_automation_receipt_response_ignored_reason(
    value: str,
) -> WebhookAutomationReceiptResponseIgnoredReason:
    if value in WEBHOOK_AUTOMATION_RECEIPT_RESPONSE_IGNORED_REASON_VALUES:
        return value
    raise TypeError(
        f"Unexpected value {value!r}. Expected one of {WEBHOOK_AUTOMATION_RECEIPT_RESPONSE_IGNORED_REASON_VALUES!r}"
    )
