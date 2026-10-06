from typing import Literal

WorkflowOutboundSpecMethod = Literal["DELETE", "GET", "HEAD", "PATCH", "POST", "PUT"]

WORKFLOW_OUTBOUND_SPEC_METHOD_VALUES: set[WorkflowOutboundSpecMethod] = {
    "DELETE",
    "GET",
    "HEAD",
    "PATCH",
    "POST",
    "PUT",
}


def check_workflow_outbound_spec_method(value: str) -> WorkflowOutboundSpecMethod:
    if value in WORKFLOW_OUTBOUND_SPEC_METHOD_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {WORKFLOW_OUTBOUND_SPEC_METHOD_VALUES!r}")
