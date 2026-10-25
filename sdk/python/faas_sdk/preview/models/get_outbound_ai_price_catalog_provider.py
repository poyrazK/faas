from typing import Literal

GetOutboundAIPriceCatalogProvider = Literal["anthropic", "azure_openai", "bedrock", "gemini", "openai", "vertex"]

GET_OUTBOUND_AI_PRICE_CATALOG_PROVIDER_VALUES: set[GetOutboundAIPriceCatalogProvider] = {
    "anthropic",
    "azure_openai",
    "bedrock",
    "gemini",
    "openai",
    "vertex",
}


def check_get_outbound_ai_price_catalog_provider(value: str) -> GetOutboundAIPriceCatalogProvider:
    if value in GET_OUTBOUND_AI_PRICE_CATALOG_PROVIDER_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {GET_OUTBOUND_AI_PRICE_CATALOG_PROVIDER_VALUES!r}")
