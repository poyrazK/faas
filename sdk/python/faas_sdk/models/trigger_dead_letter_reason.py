from typing import Literal

TriggerDeadLetterReason = Literal[
    "broker_error",
    "customer_disabled",
    "exclusive_operation_rejected",
    "max_attempts",
    "payload_too_large",
    "plan_quota",
    "poison_record",
    "rate_limited",
]

TRIGGER_DEAD_LETTER_REASON_VALUES: set[TriggerDeadLetterReason] = {
    "broker_error",
    "customer_disabled",
    "exclusive_operation_rejected",
    "max_attempts",
    "payload_too_large",
    "plan_quota",
    "poison_record",
    "rate_limited",
}


def check_trigger_dead_letter_reason(value: str) -> TriggerDeadLetterReason:
    if value in TRIGGER_DEAD_LETTER_REASON_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {TRIGGER_DEAD_LETTER_REASON_VALUES!r}")
