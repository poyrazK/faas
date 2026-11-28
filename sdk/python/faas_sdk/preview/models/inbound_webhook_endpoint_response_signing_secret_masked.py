from typing import Literal

InboundWebhookEndpointResponseSigningSecretMasked = Literal["***"]

INBOUND_WEBHOOK_ENDPOINT_RESPONSE_SIGNING_SECRET_MASKED_VALUES: set[
    InboundWebhookEndpointResponseSigningSecretMasked
] = {
    "***",
}


def check_inbound_webhook_endpoint_response_signing_secret_masked(
    value: str,
) -> InboundWebhookEndpointResponseSigningSecretMasked:
    if value in INBOUND_WEBHOOK_ENDPOINT_RESPONSE_SIGNING_SECRET_MASKED_VALUES:
        return value
    raise TypeError(
        f"Unexpected value {value!r}. Expected one of {INBOUND_WEBHOOK_ENDPOINT_RESPONSE_SIGNING_SECRET_MASKED_VALUES!r}"
    )
