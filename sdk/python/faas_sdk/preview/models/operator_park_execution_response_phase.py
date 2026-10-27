from typing import Literal

OperatorParkExecutionResponsePhase = Literal[
    "acknowledged", "cancelled", "completed", "failed", "outcome_unknown", "prepared", "started"
]

OPERATOR_PARK_EXECUTION_RESPONSE_PHASE_VALUES: set[OperatorParkExecutionResponsePhase] = {
    "acknowledged",
    "cancelled",
    "completed",
    "failed",
    "outcome_unknown",
    "prepared",
    "started",
}


def check_operator_park_execution_response_phase(value: str) -> OperatorParkExecutionResponsePhase:
    if value in OPERATOR_PARK_EXECUTION_RESPONSE_PHASE_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {OPERATOR_PARK_EXECUTION_RESPONSE_PHASE_VALUES!r}")
