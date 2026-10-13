from typing import Literal

OperationWorkflowObligationReplacementReasonKind = Literal[
    "new_cycle", "recorded_decision", "recorded_reassignment", "revised_commitment"
]

OPERATION_WORKFLOW_OBLIGATION_REPLACEMENT_REASON_KIND_VALUES: set[OperationWorkflowObligationReplacementReasonKind] = {
    "new_cycle",
    "recorded_decision",
    "recorded_reassignment",
    "revised_commitment",
}


def check_operation_workflow_obligation_replacement_reason_kind(
    value: str,
) -> OperationWorkflowObligationReplacementReasonKind:
    if value in OPERATION_WORKFLOW_OBLIGATION_REPLACEMENT_REASON_KIND_VALUES:
        return value
    raise TypeError(
        f"Unexpected value {value!r}. Expected one of {OPERATION_WORKFLOW_OBLIGATION_REPLACEMENT_REASON_KIND_VALUES!r}"
    )
