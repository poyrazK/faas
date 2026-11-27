from typing import Literal

SetStoredWorkflowPublishPolicyRequestMode = Literal["coverage", "optional", "scenarios"]

SET_STORED_WORKFLOW_PUBLISH_POLICY_REQUEST_MODE_VALUES: set[SetStoredWorkflowPublishPolicyRequestMode] = {
    "coverage",
    "optional",
    "scenarios",
}


def check_set_stored_workflow_publish_policy_request_mode(value: str) -> SetStoredWorkflowPublishPolicyRequestMode:
    if value in SET_STORED_WORKFLOW_PUBLISH_POLICY_REQUEST_MODE_VALUES:
        return value
    raise TypeError(
        f"Unexpected value {value!r}. Expected one of {SET_STORED_WORKFLOW_PUBLISH_POLICY_REQUEST_MODE_VALUES!r}"
    )
