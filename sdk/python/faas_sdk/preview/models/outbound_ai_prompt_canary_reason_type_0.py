from typing import Literal

OutboundAIPromptCanaryReasonType0 = Literal["error_rate", "latency"]

OUTBOUND_AI_PROMPT_CANARY_REASON_TYPE_0_VALUES: set[OutboundAIPromptCanaryReasonType0] = {
    "error_rate",
    "latency",
}


def check_outbound_ai_prompt_canary_reason_type_0(value: str) -> OutboundAIPromptCanaryReasonType0:
    if value in OUTBOUND_AI_PROMPT_CANARY_REASON_TYPE_0_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {OUTBOUND_AI_PROMPT_CANARY_REASON_TYPE_0_VALUES!r}")
