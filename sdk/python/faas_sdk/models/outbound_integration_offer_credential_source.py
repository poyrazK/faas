from typing import Literal

OutboundIntegrationOfferCredentialSource = Literal["customer_sealed", "operator_env"]

OUTBOUND_INTEGRATION_OFFER_CREDENTIAL_SOURCE_VALUES: set[OutboundIntegrationOfferCredentialSource] = {
    "customer_sealed",
    "operator_env",
}


def check_outbound_integration_offer_credential_source(value: str) -> OutboundIntegrationOfferCredentialSource:
    if value in OUTBOUND_INTEGRATION_OFFER_CREDENTIAL_SOURCE_VALUES:
        return value
    raise TypeError(
        f"Unexpected value {value!r}. Expected one of {OUTBOUND_INTEGRATION_OFFER_CREDENTIAL_SOURCE_VALUES!r}"
    )
