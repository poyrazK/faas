from typing import Literal

EventReceiptAttemptHistoryResponseCoverage = Literal["recorded_attempts_only"]

EVENT_RECEIPT_ATTEMPT_HISTORY_RESPONSE_COVERAGE_VALUES: set[EventReceiptAttemptHistoryResponseCoverage] = {
    "recorded_attempts_only",
}


def check_event_receipt_attempt_history_response_coverage(value: str) -> EventReceiptAttemptHistoryResponseCoverage:
    if value in EVENT_RECEIPT_ATTEMPT_HISTORY_RESPONSE_COVERAGE_VALUES:
        return value
    raise TypeError(
        f"Unexpected value {value!r}. Expected one of {EVENT_RECEIPT_ATTEMPT_HISTORY_RESPONSE_COVERAGE_VALUES!r}"
    )
