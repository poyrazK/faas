from typing import Literal

OutboundAIEvaluationState = Literal["done", "failed", "pending", "running"]

OUTBOUND_AI_EVALUATION_STATE_VALUES: set[OutboundAIEvaluationState] = {
    "done",
    "failed",
    "pending",
    "running",
}


def check_outbound_ai_evaluation_state(value: str) -> OutboundAIEvaluationState:
    if value in OUTBOUND_AI_EVALUATION_STATE_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {OUTBOUND_AI_EVALUATION_STATE_VALUES!r}")
