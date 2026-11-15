from typing import Literal

OutboundAIPromptCanaryState = Literal["active", "rolled_back"]

OUTBOUND_AI_PROMPT_CANARY_STATE_VALUES: set[OutboundAIPromptCanaryState] = {
    "active",
    "rolled_back",
}


def check_outbound_ai_prompt_canary_state(value: str) -> OutboundAIPromptCanaryState:
    if value in OUTBOUND_AI_PROMPT_CANARY_STATE_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {OUTBOUND_AI_PROMPT_CANARY_STATE_VALUES!r}")
