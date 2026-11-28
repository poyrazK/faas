from typing import Literal

TaskToolReleaseReceiptPendingSubmission = Literal["web", "worker"]

TASK_TOOL_RELEASE_RECEIPT_PENDING_SUBMISSION_VALUES: set[TaskToolReleaseReceiptPendingSubmission] = {
    "web",
    "worker",
}


def check_task_tool_release_receipt_pending_submission(value: str) -> TaskToolReleaseReceiptPendingSubmission:
    if value in TASK_TOOL_RELEASE_RECEIPT_PENDING_SUBMISSION_VALUES:
        return value
    raise TypeError(
        f"Unexpected value {value!r}. Expected one of {TASK_TOOL_RELEASE_RECEIPT_PENDING_SUBMISSION_VALUES!r}"
    )
