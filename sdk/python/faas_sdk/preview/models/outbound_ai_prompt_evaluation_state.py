from typing import Literal

OutboundAIPromptEvaluationState = Literal["done", "failed", "pending", "running"]

OUTBOUND_AI_PROMPT_EVALUATION_STATE_VALUES: set[OutboundAIPromptEvaluationState] = {
    "done",
    "failed",
    "pending",
    "running",
}


def check_outbound_ai_prompt_evaluation_state(value: str) -> OutboundAIPromptEvaluationState:
    if value in OUTBOUND_AI_PROMPT_EVALUATION_STATE_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {OUTBOUND_AI_PROMPT_EVALUATION_STATE_VALUES!r}")
