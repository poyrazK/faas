from typing import Literal

ObjectStorageUsageResponseBillingMode = Literal["live", "off", "shadow"]

OBJECT_STORAGE_USAGE_RESPONSE_BILLING_MODE_VALUES: set[ObjectStorageUsageResponseBillingMode] = {
    "live",
    "off",
    "shadow",
}


def check_object_storage_usage_response_billing_mode(value: str) -> ObjectStorageUsageResponseBillingMode:
    if value in OBJECT_STORAGE_USAGE_RESPONSE_BILLING_MODE_VALUES:
        return value
    raise TypeError(
        f"Unexpected value {value!r}. Expected one of {OBJECT_STORAGE_USAGE_RESPONSE_BILLING_MODE_VALUES!r}"
    )
