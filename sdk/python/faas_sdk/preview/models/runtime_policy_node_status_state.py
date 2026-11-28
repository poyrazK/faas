from typing import Literal

RuntimePolicyNodeStatusState = Literal["active", "pending", "unverified"]

RUNTIME_POLICY_NODE_STATUS_STATE_VALUES: set[RuntimePolicyNodeStatusState] = {
    "active",
    "pending",
    "unverified",
}


def check_runtime_policy_node_status_state(value: str) -> RuntimePolicyNodeStatusState:
    if value in RUNTIME_POLICY_NODE_STATUS_STATE_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {RUNTIME_POLICY_NODE_STATUS_STATE_VALUES!r}")
