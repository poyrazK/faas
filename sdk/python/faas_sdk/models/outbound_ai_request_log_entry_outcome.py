from typing import Literal

OutboundAIRequestLogEntryOutcome = Literal["error", "known", "unknown"]

OUTBOUND_AI_REQUEST_LOG_ENTRY_OUTCOME_VALUES: set[OutboundAIRequestLogEntryOutcome] = {
    "error",
    "known",
    "unknown",
}


def check_outbound_ai_request_log_entry_outcome(value: str) -> OutboundAIRequestLogEntryOutcome:
    if value in OUTBOUND_AI_REQUEST_LOG_ENTRY_OUTCOME_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {OUTBOUND_AI_REQUEST_LOG_ENTRY_OUTCOME_VALUES!r}")
