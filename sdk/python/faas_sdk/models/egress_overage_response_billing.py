from typing import Literal

EgressOverageResponseBilling = Literal["live", "off"]

EGRESS_OVERAGE_RESPONSE_BILLING_VALUES: set[EgressOverageResponseBilling] = {
    "live",
    "off",
}


def check_egress_overage_response_billing(value: str) -> EgressOverageResponseBilling:
    if value in EGRESS_OVERAGE_RESPONSE_BILLING_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {EGRESS_OVERAGE_RESPONSE_BILLING_VALUES!r}")
