from typing import Literal

OutboundIntegrationOfferAiProvider = Literal["anthropic", "azure_openai", "bedrock", "gemini", "openai", "vertex"]

OUTBOUND_INTEGRATION_OFFER_AI_PROVIDER_VALUES: set[OutboundIntegrationOfferAiProvider] = {
    "anthropic",
    "azure_openai",
    "bedrock",
    "gemini",
    "openai",
    "vertex",
}


def check_outbound_integration_offer_ai_provider(value: str) -> OutboundIntegrationOfferAiProvider:
    if value in OUTBOUND_INTEGRATION_OFFER_AI_PROVIDER_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {OUTBOUND_INTEGRATION_OFFER_AI_PROVIDER_VALUES!r}")
