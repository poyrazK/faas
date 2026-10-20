from typing import Literal

AdminRefundResponseProvider = Literal["polar"]

ADMIN_REFUND_RESPONSE_PROVIDER_VALUES: set[AdminRefundResponseProvider] = {
    "polar",
}


def check_admin_refund_response_provider(value: str) -> AdminRefundResponseProvider:
    if value in ADMIN_REFUND_RESPONSE_PROVIDER_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {ADMIN_REFUND_RESPONSE_PROVIDER_VALUES!r}")
