from typing import Literal

OutboundAIPromptMessageRole = Literal["assistant", "developer", "system", "user"]

OUTBOUND_AI_PROMPT_MESSAGE_ROLE_VALUES: set[OutboundAIPromptMessageRole] = {
    "assistant",
    "developer",
    "system",
    "user",
}


def check_outbound_ai_prompt_message_role(value: str) -> OutboundAIPromptMessageRole:
    if value in OUTBOUND_AI_PROMPT_MESSAGE_ROLE_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {OUTBOUND_AI_PROMPT_MESSAGE_ROLE_VALUES!r}")
