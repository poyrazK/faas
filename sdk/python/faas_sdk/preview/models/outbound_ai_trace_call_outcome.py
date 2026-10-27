from typing import Literal

OutboundAITraceCallOutcome = Literal["error", "known", "unknown"]

OUTBOUND_AI_TRACE_CALL_OUTCOME_VALUES: set[OutboundAITraceCallOutcome] = {
    "error",
    "known",
    "unknown",
}


def check_outbound_ai_trace_call_outcome(value: str) -> OutboundAITraceCallOutcome:
    if value in OUTBOUND_AI_TRACE_CALL_OUTCOME_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {OUTBOUND_AI_TRACE_CALL_OUTCOME_VALUES!r}")
