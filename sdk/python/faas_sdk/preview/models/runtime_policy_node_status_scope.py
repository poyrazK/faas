from typing import Literal

RuntimePolicyNodeStatusScope = Literal["app"]

RUNTIME_POLICY_NODE_STATUS_SCOPE_VALUES: set[RuntimePolicyNodeStatusScope] = {
    "app",
}


def check_runtime_policy_node_status_scope(value: str) -> RuntimePolicyNodeStatusScope:
    if value in RUNTIME_POLICY_NODE_STATUS_SCOPE_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {RUNTIME_POLICY_NODE_STATUS_SCOPE_VALUES!r}")
