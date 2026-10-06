from typing import Literal

CreateInboundWebhookEndpointRequestProvider = Literal["generic", "stripe"]

CREATE_INBOUND_WEBHOOK_ENDPOINT_REQUEST_PROVIDER_VALUES: set[CreateInboundWebhookEndpointRequestProvider] = {
    "generic",
    "stripe",
}


def check_create_inbound_webhook_endpoint_request_provider(value: str) -> CreateInboundWebhookEndpointRequestProvider:
    if value in CREATE_INBOUND_WEBHOOK_ENDPOINT_REQUEST_PROVIDER_VALUES:
        return value
    raise TypeError(
        f"Unexpected value {value!r}. Expected one of {CREATE_INBOUND_WEBHOOK_ENDPOINT_REQUEST_PROVIDER_VALUES!r}"
    )
