from typing import Literal

ExecutionResultValidationStatus = Literal["invalid", "valid"]

EXECUTION_RESULT_VALIDATION_STATUS_VALUES: set[ExecutionResultValidationStatus] = {
    "invalid",
    "valid",
}


def check_execution_result_validation_status(value: str) -> ExecutionResultValidationStatus:
    if value in EXECUTION_RESULT_VALIDATION_STATUS_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {EXECUTION_RESULT_VALIDATION_STATUS_VALUES!r}")
