from typing import Literal

PutOutboundAIPolicyRequestAiProvider = Literal["", "anthropic", "azure_openai", "bedrock", "gemini", "openai", "vertex"]

PUT_OUTBOUND_AI_POLICY_REQUEST_AI_PROVIDER_VALUES: set[PutOutboundAIPolicyRequestAiProvider] = {
    "",
    "anthropic",
    "azure_openai",
    "bedrock",
    "gemini",
    "openai",
    "vertex",
}


def check_put_outbound_ai_policy_request_ai_provider(value: str) -> PutOutboundAIPolicyRequestAiProvider:
    if value in PUT_OUTBOUND_AI_POLICY_REQUEST_AI_PROVIDER_VALUES:
        return value
    raise TypeError(
        f"Unexpected value {value!r}. Expected one of {PUT_OUTBOUND_AI_POLICY_REQUEST_AI_PROVIDER_VALUES!r}"
    )
