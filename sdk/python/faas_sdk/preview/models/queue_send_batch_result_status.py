from typing import Literal

QueueSendBatchResultStatus = Literal["accepted", "not_attempted", "rejected"]

QUEUE_SEND_BATCH_RESULT_STATUS_VALUES: set[QueueSendBatchResultStatus] = {
    "accepted",
    "not_attempted",
    "rejected",
}


def check_queue_send_batch_result_status(value: str) -> QueueSendBatchResultStatus:
    if value in QUEUE_SEND_BATCH_RESULT_STATUS_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {QUEUE_SEND_BATCH_RESULT_STATUS_VALUES!r}")
