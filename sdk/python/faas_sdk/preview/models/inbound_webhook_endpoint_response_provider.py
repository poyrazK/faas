from typing import Literal

InboundWebhookEndpointResponseProvider = Literal["generic", "stripe"]

INBOUND_WEBHOOK_ENDPOINT_RESPONSE_PROVIDER_VALUES: set[InboundWebhookEndpointResponseProvider] = {
    "generic",
    "stripe",
}


def check_inbound_webhook_endpoint_response_provider(value: str) -> InboundWebhookEndpointResponseProvider:
    if value in INBOUND_WEBHOOK_ENDPOINT_RESPONSE_PROVIDER_VALUES:
        return value
    raise TypeError(
        f"Unexpected value {value!r}. Expected one of {INBOUND_WEBHOOK_ENDPOINT_RESPONSE_PROVIDER_VALUES!r}"
    )
