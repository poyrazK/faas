from typing import Literal

CreateOutboundIntegrationRequestAiProvider = Literal[
    "anthropic", "azure_openai", "bedrock", "gemini", "openai", "vertex"
]

CREATE_OUTBOUND_INTEGRATION_REQUEST_AI_PROVIDER_VALUES: set[CreateOutboundIntegrationRequestAiProvider] = {
    "anthropic",
    "azure_openai",
    "bedrock",
    "gemini",
    "openai",
    "vertex",
}


def check_create_outbound_integration_request_ai_provider(value: str) -> CreateOutboundIntegrationRequestAiProvider:
    if value in CREATE_OUTBOUND_INTEGRATION_REQUEST_AI_PROVIDER_VALUES:
        return value
    raise TypeError(
        f"Unexpected value {value!r}. Expected one of {CREATE_OUTBOUND_INTEGRATION_REQUEST_AI_PROVIDER_VALUES!r}"
    )
