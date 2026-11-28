from typing import Literal

ProfileCanaryGateDecisionOnTimeout = Literal["continue", "hold"]

PROFILE_CANARY_GATE_DECISION_ON_TIMEOUT_VALUES: set[ProfileCanaryGateDecisionOnTimeout] = {
    "continue",
    "hold",
}


def check_profile_canary_gate_decision_on_timeout(value: str) -> ProfileCanaryGateDecisionOnTimeout:
    if value in PROFILE_CANARY_GATE_DECISION_ON_TIMEOUT_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {PROFILE_CANARY_GATE_DECISION_ON_TIMEOUT_VALUES!r}")
