from typing import Literal

OperatorRestartExecutionResponsePhase = Literal[
    "completed", "destroyed", "failed", "outcome_unknown", "prepared", "started"
]

OPERATOR_RESTART_EXECUTION_RESPONSE_PHASE_VALUES: set[OperatorRestartExecutionResponsePhase] = {
    "completed",
    "destroyed",
    "failed",
    "outcome_unknown",
    "prepared",
    "started",
}


def check_operator_restart_execution_response_phase(value: str) -> OperatorRestartExecutionResponsePhase:
    if value in OPERATOR_RESTART_EXECUTION_RESPONSE_PHASE_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {OPERATOR_RESTART_EXECUTION_RESPONSE_PHASE_VALUES!r}")
