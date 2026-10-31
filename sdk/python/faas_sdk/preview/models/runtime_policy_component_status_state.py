from typing import Literal

RuntimePolicyComponentStatusState = Literal["active", "pending", "unverified"]

RUNTIME_POLICY_COMPONENT_STATUS_STATE_VALUES: set[RuntimePolicyComponentStatusState] = {
    "active",
    "pending",
    "unverified",
}


def check_runtime_policy_component_status_state(value: str) -> RuntimePolicyComponentStatusState:
    if value in RUNTIME_POLICY_COMPONENT_STATUS_STATE_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {RUNTIME_POLICY_COMPONENT_STATUS_STATE_VALUES!r}")
