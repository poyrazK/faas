from typing import Literal

OperatorParkExecutionResponseAction = Literal["capture", "stop"]

OPERATOR_PARK_EXECUTION_RESPONSE_ACTION_VALUES: set[OperatorParkExecutionResponseAction] = {
    "capture",
    "stop",
}


def check_operator_park_execution_response_action(value: str) -> OperatorParkExecutionResponseAction:
    if value in OPERATOR_PARK_EXECUTION_RESPONSE_ACTION_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {OPERATOR_PARK_EXECUTION_RESPONSE_ACTION_VALUES!r}")
