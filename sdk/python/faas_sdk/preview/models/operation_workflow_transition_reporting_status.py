from typing import Literal

OperationWorkflowTransitionReportingStatus = Literal[
    "ambiguous", "compared", "definition_unavailable", "incomplete", "limit", "terminal", "unconfigured"
]

OPERATION_WORKFLOW_TRANSITION_REPORTING_STATUS_VALUES: set[OperationWorkflowTransitionReportingStatus] = {
    "ambiguous",
    "compared",
    "definition_unavailable",
    "incomplete",
    "limit",
    "terminal",
    "unconfigured",
}


def check_operation_workflow_transition_reporting_status(value: str) -> OperationWorkflowTransitionReportingStatus:
    if value in OPERATION_WORKFLOW_TRANSITION_REPORTING_STATUS_VALUES:
        return value
    raise TypeError(
        f"Unexpected value {value!r}. Expected one of {OPERATION_WORKFLOW_TRANSITION_REPORTING_STATUS_VALUES!r}"
    )
