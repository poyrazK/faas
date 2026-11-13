from typing import Literal

StoredWorkflowPublishPolicyMode = Literal["coverage", "optional", "scenarios"]

STORED_WORKFLOW_PUBLISH_POLICY_MODE_VALUES: set[StoredWorkflowPublishPolicyMode] = {
    "coverage",
    "optional",
    "scenarios",
}


def check_stored_workflow_publish_policy_mode(value: str) -> StoredWorkflowPublishPolicyMode:
    if value in STORED_WORKFLOW_PUBLISH_POLICY_MODE_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {STORED_WORKFLOW_PUBLISH_POLICY_MODE_VALUES!r}")
