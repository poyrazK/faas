from typing import Literal

OutboundIntegrationOfferOwnerKind = Literal["customer", "operator"]

OUTBOUND_INTEGRATION_OFFER_OWNER_KIND_VALUES: set[OutboundIntegrationOfferOwnerKind] = {
    "customer",
    "operator",
}


def check_outbound_integration_offer_owner_kind(value: str) -> OutboundIntegrationOfferOwnerKind:
    if value in OUTBOUND_INTEGRATION_OFFER_OWNER_KIND_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {OUTBOUND_INTEGRATION_OFFER_OWNER_KIND_VALUES!r}")
