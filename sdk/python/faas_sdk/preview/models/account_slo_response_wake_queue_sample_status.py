from typing import Literal

AccountSLOResponseWakeQueueSampleStatus = Literal["available", "no_sample", "unavailable"]

ACCOUNT_SLO_RESPONSE_WAKE_QUEUE_SAMPLE_STATUS_VALUES: set[AccountSLOResponseWakeQueueSampleStatus] = {
    "available",
    "no_sample",
    "unavailable",
}


def check_account_slo_response_wake_queue_sample_status(value: str) -> AccountSLOResponseWakeQueueSampleStatus:
    if value in ACCOUNT_SLO_RESPONSE_WAKE_QUEUE_SAMPLE_STATUS_VALUES:
        return value
    raise TypeError(
        f"Unexpected value {value!r}. Expected one of {ACCOUNT_SLO_RESPONSE_WAKE_QUEUE_SAMPLE_STATUS_VALUES!r}"
    )
