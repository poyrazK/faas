from typing import Literal

ProfileCanaryGateDecisionStatus = Literal[
    "collecting", "disabled", "overridden", "passed", "regressed", "rolled_back", "timed_out"
]

PROFILE_CANARY_GATE_DECISION_STATUS_VALUES: set[ProfileCanaryGateDecisionStatus] = {
    "collecting",
    "disabled",
    "overridden",
    "passed",
    "regressed",
    "rolled_back",
    "timed_out",
}


def check_profile_canary_gate_decision_status(value: str) -> ProfileCanaryGateDecisionStatus:
    if value in PROFILE_CANARY_GATE_DECISION_STATUS_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {PROFILE_CANARY_GATE_DECISION_STATUS_VALUES!r}")
