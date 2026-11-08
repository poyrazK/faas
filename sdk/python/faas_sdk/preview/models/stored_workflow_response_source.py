from typing import Literal

StoredWorkflowResponseSource = Literal["dashboard", "draft", "manifest"]

STORED_WORKFLOW_RESPONSE_SOURCE_VALUES: set[StoredWorkflowResponseSource] = {
    "dashboard",
    "draft",
    "manifest",
}


def check_stored_workflow_response_source(value: str) -> StoredWorkflowResponseSource:
    if value in STORED_WORKFLOW_RESPONSE_SOURCE_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {STORED_WORKFLOW_RESPONSE_SOURCE_VALUES!r}")
