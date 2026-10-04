from typing import Literal

CreateManagedExecutionWorkflowRequestFailurePolicy = Literal["continue_independent", "fail_fast"]

CREATE_MANAGED_EXECUTION_WORKFLOW_REQUEST_FAILURE_POLICY_VALUES: set[
    CreateManagedExecutionWorkflowRequestFailurePolicy
] = {
    "continue_independent",
    "fail_fast",
}


def check_create_managed_execution_workflow_request_failure_policy(
    value: str,
) -> CreateManagedExecutionWorkflowRequestFailurePolicy:
    if value in CREATE_MANAGED_EXECUTION_WORKFLOW_REQUEST_FAILURE_POLICY_VALUES:
        return value
    raise TypeError(
        f"Unexpected value {value!r}. Expected one of {CREATE_MANAGED_EXECUTION_WORKFLOW_REQUEST_FAILURE_POLICY_VALUES!r}"
    )
