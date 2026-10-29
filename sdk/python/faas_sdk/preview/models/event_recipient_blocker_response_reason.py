from typing import Literal

EventRecipientBlockerResponseReason = Literal[
    "acceptance_replaced",
    "receipt_unavailable",
    "recipient_unavailable",
    "routing_evidence_unavailable",
    "snapshot_unavailable",
]

EVENT_RECIPIENT_BLOCKER_RESPONSE_REASON_VALUES: set[EventRecipientBlockerResponseReason] = {
    "acceptance_replaced",
    "receipt_unavailable",
    "recipient_unavailable",
    "routing_evidence_unavailable",
    "snapshot_unavailable",
}


def check_event_recipient_blocker_response_reason(value: str) -> EventRecipientBlockerResponseReason:
    if value in EVENT_RECIPIENT_BLOCKER_RESPONSE_REASON_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {EVENT_RECIPIENT_BLOCKER_RESPONSE_REASON_VALUES!r}")
