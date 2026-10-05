from typing import Literal

WorkflowGuardSpecOp = Literal["eq", "exists", "gt", "gte", "lt", "lte", "ne"]

WORKFLOW_GUARD_SPEC_OP_VALUES: set[WorkflowGuardSpecOp] = {
    "eq",
    "exists",
    "gt",
    "gte",
    "lt",
    "lte",
    "ne",
}


def check_workflow_guard_spec_op(value: str) -> WorkflowGuardSpecOp:
    if value in WORKFLOW_GUARD_SPEC_OP_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {WORKFLOW_GUARD_SPEC_OP_VALUES!r}")
