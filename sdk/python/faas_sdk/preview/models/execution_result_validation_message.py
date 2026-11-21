from typing import Literal

ExecutionResultValidationMessage = Literal["Run result does not match result_schema"]

EXECUTION_RESULT_VALIDATION_MESSAGE_VALUES: set[ExecutionResultValidationMessage] = {
    "Run result does not match result_schema",
}


def check_execution_result_validation_message(value: str) -> ExecutionResultValidationMessage:
    if value in EXECUTION_RESULT_VALIDATION_MESSAGE_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {EXECUTION_RESULT_VALIDATION_MESSAGE_VALUES!r}")
