from typing import Literal

OperationWorkflowResolutionVerificationStatus = Literal["awaiting_verification", "verified"]

OPERATION_WORKFLOW_RESOLUTION_VERIFICATION_STATUS_VALUES: set[OperationWorkflowResolutionVerificationStatus] = {
    "awaiting_verification",
    "verified",
}


def check_operation_workflow_resolution_verification_status(
    value: str,
) -> OperationWorkflowResolutionVerificationStatus:
    if value in OPERATION_WORKFLOW_RESOLUTION_VERIFICATION_STATUS_VALUES:
        return value
    raise TypeError(
        f"Unexpected value {value!r}. Expected one of {OPERATION_WORKFLOW_RESOLUTION_VERIFICATION_STATUS_VALUES!r}"
    )
