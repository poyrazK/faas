from typing import Literal

AccountTraceWakeRestoreFallbackReason = Literal["after_restore_failed"]

ACCOUNT_TRACE_WAKE_RESTORE_FALLBACK_REASON_VALUES: set[AccountTraceWakeRestoreFallbackReason] = {
    "after_restore_failed",
}


def check_account_trace_wake_restore_fallback_reason(value: str) -> AccountTraceWakeRestoreFallbackReason:
    if value in ACCOUNT_TRACE_WAKE_RESTORE_FALLBACK_REASON_VALUES:
        return value
    raise TypeError(
        f"Unexpected value {value!r}. Expected one of {ACCOUNT_TRACE_WAKE_RESTORE_FALLBACK_REASON_VALUES!r}"
    )
