from typing import Literal

RuntimePolicyStatusResponseState = Literal["active", "pending", "unverified"]

RUNTIME_POLICY_STATUS_RESPONSE_STATE_VALUES: set[RuntimePolicyStatusResponseState] = {
    "active",
    "pending",
    "unverified",
}


def check_runtime_policy_status_response_state(value: str) -> RuntimePolicyStatusResponseState:
    if value in RUNTIME_POLICY_STATUS_RESPONSE_STATE_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {RUNTIME_POLICY_STATUS_RESPONSE_STATE_VALUES!r}")
