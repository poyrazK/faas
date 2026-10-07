from typing import Literal

QueueGlobalQuotaDeliveryRequestPurpose = Literal["grant", "retirement"]

QUEUE_GLOBAL_QUOTA_DELIVERY_REQUEST_PURPOSE_VALUES: set[QueueGlobalQuotaDeliveryRequestPurpose] = {
    "grant",
    "retirement",
}


def check_queue_global_quota_delivery_request_purpose(value: str) -> QueueGlobalQuotaDeliveryRequestPurpose:
    if value in QUEUE_GLOBAL_QUOTA_DELIVERY_REQUEST_PURPOSE_VALUES:
        return value
    raise TypeError(
        f"Unexpected value {value!r}. Expected one of {QUEUE_GLOBAL_QUOTA_DELIVERY_REQUEST_PURPOSE_VALUES!r}"
    )
