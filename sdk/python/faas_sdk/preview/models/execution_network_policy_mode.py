from typing import Literal

ExecutionNetworkPolicyMode = Literal["none"]

EXECUTION_NETWORK_POLICY_MODE_VALUES: set[ExecutionNetworkPolicyMode] = {
    "none",
}


def check_execution_network_policy_mode(value: str) -> ExecutionNetworkPolicyMode:
    if value in EXECUTION_NETWORK_POLICY_MODE_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {EXECUTION_NETWORK_POLICY_MODE_VALUES!r}")
