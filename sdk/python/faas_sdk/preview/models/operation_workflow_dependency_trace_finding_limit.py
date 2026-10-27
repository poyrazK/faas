from typing import Literal

OperationWorkflowDependencyTraceFindingLimit = Literal[128]

OPERATION_WORKFLOW_DEPENDENCY_TRACE_FINDING_LIMIT_VALUES: set[OperationWorkflowDependencyTraceFindingLimit] = {
    128,
}


def check_operation_workflow_dependency_trace_finding_limit(value: int) -> OperationWorkflowDependencyTraceFindingLimit:
    if value in OPERATION_WORKFLOW_DEPENDENCY_TRACE_FINDING_LIMIT_VALUES:
        return value
    raise TypeError(
        f"Unexpected value {value!r}. Expected one of {OPERATION_WORKFLOW_DEPENDENCY_TRACE_FINDING_LIMIT_VALUES!r}"
    )
