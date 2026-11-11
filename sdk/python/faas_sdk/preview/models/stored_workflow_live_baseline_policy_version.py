from typing import Literal

StoredWorkflowLiveBaselinePolicyVersion = Literal[2]

STORED_WORKFLOW_LIVE_BASELINE_POLICY_VERSION_VALUES: set[StoredWorkflowLiveBaselinePolicyVersion] = {
    2,
}


def check_stored_workflow_live_baseline_policy_version(value: int) -> StoredWorkflowLiveBaselinePolicyVersion:
    if value in STORED_WORKFLOW_LIVE_BASELINE_POLICY_VERSION_VALUES:
        return value
    raise TypeError(
        f"Unexpected value {value!r}. Expected one of {STORED_WORKFLOW_LIVE_BASELINE_POLICY_VERSION_VALUES!r}"
    )
