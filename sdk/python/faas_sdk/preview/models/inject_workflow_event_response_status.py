from typing import Literal

InjectWorkflowEventResponseStatus = Literal["received"]

INJECT_WORKFLOW_EVENT_RESPONSE_STATUS_VALUES: set[InjectWorkflowEventResponseStatus] = {
    "received",
}


def check_inject_workflow_event_response_status(value: str) -> InjectWorkflowEventResponseStatus:
    if value in INJECT_WORKFLOW_EVENT_RESPONSE_STATUS_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {INJECT_WORKFLOW_EVENT_RESPONSE_STATUS_VALUES!r}")
