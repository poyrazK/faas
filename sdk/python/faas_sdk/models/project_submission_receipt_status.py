from typing import Literal

ProjectSubmissionReceiptStatus = Literal["complete", "interrupted", "processing", "rejected"]

PROJECT_SUBMISSION_RECEIPT_STATUS_VALUES: set[ProjectSubmissionReceiptStatus] = {
    "complete",
    "interrupted",
    "processing",
    "rejected",
}


def check_project_submission_receipt_status(value: str) -> ProjectSubmissionReceiptStatus:
    if value in PROJECT_SUBMISSION_RECEIPT_STATUS_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {PROJECT_SUBMISSION_RECEIPT_STATUS_VALUES!r}")
