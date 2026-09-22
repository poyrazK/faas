from typing import Literal

CreateInboundWebhookEndpointRequestProvider = Literal["stripe"]

CREATE_INBOUND_WEBHOOK_ENDPOINT_REQUEST_PROVIDER_VALUES: set[CreateInboundWebhookEndpointRequestProvider] = {
    "stripe",
}


def check_create_inbound_webhook_endpoint_request_provider(value: str) -> CreateInboundWebhookEndpointRequestProvider:
    if value in CREATE_INBOUND_WEBHOOK_ENDPOINT_REQUEST_PROVIDER_VALUES:
        return value
    raise TypeError(
        f"Unexpected value {value!r}. Expected one of {CREATE_INBOUND_WEBHOOK_ENDPOINT_REQUEST_PROVIDER_VALUES!r}"
    )
