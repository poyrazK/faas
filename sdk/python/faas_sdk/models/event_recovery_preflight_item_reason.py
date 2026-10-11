from typing import Literal

EventRecoveryPreflightItemReason = Literal[
    "capacity", "changed", "eligible", "expired", "legacy_claim", "receipt_expired", "target_unavailable", "unknown"
]

EVENT_RECOVERY_PREFLIGHT_ITEM_REASON_VALUES: set[EventRecoveryPreflightItemReason] = {
    "capacity",
    "changed",
    "eligible",
    "expired",
    "legacy_claim",
    "receipt_expired",
    "target_unavailable",
    "unknown",
}


def check_event_recovery_preflight_item_reason(value: str) -> EventRecoveryPreflightItemReason:
    if value in EVENT_RECOVERY_PREFLIGHT_ITEM_REASON_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {EVENT_RECOVERY_PREFLIGHT_ITEM_REASON_VALUES!r}")
