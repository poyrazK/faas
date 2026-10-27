from typing import Literal

OperatorParkExecutionResponseTerminalState = Literal["parked", "stopped"]

OPERATOR_PARK_EXECUTION_RESPONSE_TERMINAL_STATE_VALUES: set[OperatorParkExecutionResponseTerminalState] = {
    "parked",
    "stopped",
}


def check_operator_park_execution_response_terminal_state(value: str) -> OperatorParkExecutionResponseTerminalState:
    if value in OPERATOR_PARK_EXECUTION_RESPONSE_TERMINAL_STATE_VALUES:
        return value
    raise TypeError(
        f"Unexpected value {value!r}. Expected one of {OPERATOR_PARK_EXECUTION_RESPONSE_TERMINAL_STATE_VALUES!r}"
    )
