from typing import Literal

PublishEventBatchResultStatus = Literal["accepted", "duplicate", "rejected", "unknown"]

PUBLISH_EVENT_BATCH_RESULT_STATUS_VALUES: set[PublishEventBatchResultStatus] = {
    "accepted",
    "duplicate",
    "rejected",
    "unknown",
}


def check_publish_event_batch_result_status(value: str) -> PublishEventBatchResultStatus:
    if value in PUBLISH_EVENT_BATCH_RESULT_STATUS_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {PUBLISH_EVENT_BATCH_RESULT_STATUS_VALUES!r}")
