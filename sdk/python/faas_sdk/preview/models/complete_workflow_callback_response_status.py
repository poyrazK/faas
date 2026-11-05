from typing import Literal

CompleteWorkflowCallbackResponseStatus = Literal["received"]

COMPLETE_WORKFLOW_CALLBACK_RESPONSE_STATUS_VALUES: set[CompleteWorkflowCallbackResponseStatus] = {
    "received",
}


def check_complete_workflow_callback_response_status(value: str) -> CompleteWorkflowCallbackResponseStatus:
    if value in COMPLETE_WORKFLOW_CALLBACK_RESPONSE_STATUS_VALUES:
        return value
    raise TypeError(
        f"Unexpected value {value!r}. Expected one of {COMPLETE_WORKFLOW_CALLBACK_RESPONSE_STATUS_VALUES!r}"
    )
