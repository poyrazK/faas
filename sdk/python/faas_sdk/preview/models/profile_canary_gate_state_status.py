from typing import Literal

ProfileCanaryGateStateStatus = Literal["collecting", "inconclusive", "passed", "regressed", "timed_out"]

PROFILE_CANARY_GATE_STATE_STATUS_VALUES: set[ProfileCanaryGateStateStatus] = {
    "collecting",
    "inconclusive",
    "passed",
    "regressed",
    "timed_out",
}


def check_profile_canary_gate_state_status(value: str) -> ProfileCanaryGateStateStatus:
    if value in PROFILE_CANARY_GATE_STATE_STATUS_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {PROFILE_CANARY_GATE_STATE_STATUS_VALUES!r}")
