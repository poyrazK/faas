from typing import Literal

GlobalQuotaDeliveryResponsePurpose = Literal["grant", "retirement"]

GLOBAL_QUOTA_DELIVERY_RESPONSE_PURPOSE_VALUES: set[GlobalQuotaDeliveryResponsePurpose] = {
    "grant",
    "retirement",
}


def check_global_quota_delivery_response_purpose(value: str) -> GlobalQuotaDeliveryResponsePurpose:
    if value in GLOBAL_QUOTA_DELIVERY_RESPONSE_PURPOSE_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {GLOBAL_QUOTA_DELIVERY_RESPONSE_PURPOSE_VALUES!r}")
