from typing import Literal

AppSLOResponseWakeQueueSampleStatus = Literal["available", "no_sample", "unavailable"]

APP_SLO_RESPONSE_WAKE_QUEUE_SAMPLE_STATUS_VALUES: set[AppSLOResponseWakeQueueSampleStatus] = {
    "available",
    "no_sample",
    "unavailable",
}


def check_app_slo_response_wake_queue_sample_status(value: str) -> AppSLOResponseWakeQueueSampleStatus:
    if value in APP_SLO_RESPONSE_WAKE_QUEUE_SAMPLE_STATUS_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {APP_SLO_RESPONSE_WAKE_QUEUE_SAMPLE_STATUS_VALUES!r}")
