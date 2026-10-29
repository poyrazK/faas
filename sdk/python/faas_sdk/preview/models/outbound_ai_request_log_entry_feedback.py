from typing import Literal

OutboundAIRequestLogEntryFeedback = Literal["bad", "good"]

OUTBOUND_AI_REQUEST_LOG_ENTRY_FEEDBACK_VALUES: set[OutboundAIRequestLogEntryFeedback] = {
    "bad",
    "good",
}


def check_outbound_ai_request_log_entry_feedback(value: str) -> OutboundAIRequestLogEntryFeedback:
    if value in OUTBOUND_AI_REQUEST_LOG_ENTRY_FEEDBACK_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {OUTBOUND_AI_REQUEST_LOG_ENTRY_FEEDBACK_VALUES!r}")
