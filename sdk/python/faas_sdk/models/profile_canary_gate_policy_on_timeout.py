from typing import Literal

ProfileCanaryGatePolicyOnTimeout = Literal["continue", "hold"]

PROFILE_CANARY_GATE_POLICY_ON_TIMEOUT_VALUES: set[ProfileCanaryGatePolicyOnTimeout] = {
    "continue",
    "hold",
}


def check_profile_canary_gate_policy_on_timeout(value: str) -> ProfileCanaryGatePolicyOnTimeout:
    if value in PROFILE_CANARY_GATE_POLICY_ON_TIMEOUT_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {PROFILE_CANARY_GATE_POLICY_ON_TIMEOUT_VALUES!r}")
