from typing import Literal

WorkflowRecoveryCancellationResponseOutcome = Literal[
    "already_cancelled",
    "already_terminal",
    "cancelled",
    "changed",
    "eligible",
    "not_admitted",
    "operation_owned",
    "unknown",
]

WORKFLOW_RECOVERY_CANCELLATION_RESPONSE_OUTCOME_VALUES: set[WorkflowRecoveryCancellationResponseOutcome] = {
    "already_cancelled",
    "already_terminal",
    "cancelled",
    "changed",
    "eligible",
    "not_admitted",
    "operation_owned",
    "unknown",
}


def check_workflow_recovery_cancellation_response_outcome(value: str) -> WorkflowRecoveryCancellationResponseOutcome:
    if value in WORKFLOW_RECOVERY_CANCELLATION_RESPONSE_OUTCOME_VALUES:
        return value
    raise TypeError(
        f"Unexpected value {value!r}. Expected one of {WORKFLOW_RECOVERY_CANCELLATION_RESPONSE_OUTCOME_VALUES!r}"
    )
