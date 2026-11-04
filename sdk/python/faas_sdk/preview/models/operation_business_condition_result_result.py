from typing import Literal

OperationBusinessConditionResultResult = Literal["fail", "pass", "unknown"]

OPERATION_BUSINESS_CONDITION_RESULT_RESULT_VALUES: set[OperationBusinessConditionResultResult] = {
    "fail",
    "pass",
    "unknown",
}


def check_operation_business_condition_result_result(value: str) -> OperationBusinessConditionResultResult:
    if value in OPERATION_BUSINESS_CONDITION_RESULT_RESULT_VALUES:
        return value
    raise TypeError(
        f"Unexpected value {value!r}. Expected one of {OPERATION_BUSINESS_CONDITION_RESULT_RESULT_VALUES!r}"
    )
