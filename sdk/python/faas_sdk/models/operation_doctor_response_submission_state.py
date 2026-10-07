from typing import Literal

OperationDoctorResponseSubmissionState = Literal["blocked", "eligible", "unknown"]

OPERATION_DOCTOR_RESPONSE_SUBMISSION_STATE_VALUES: set[OperationDoctorResponseSubmissionState] = {
    "blocked",
    "eligible",
    "unknown",
}


def check_operation_doctor_response_submission_state(value: str) -> OperationDoctorResponseSubmissionState:
    if value in OPERATION_DOCTOR_RESPONSE_SUBMISSION_STATE_VALUES:
        return value
    raise TypeError(
        f"Unexpected value {value!r}. Expected one of {OPERATION_DOCTOR_RESPONSE_SUBMISSION_STATE_VALUES!r}"
    )
