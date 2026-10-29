from typing import Literal

DebugRolloutExplanationState = Literal["aborted", "complete", "held", "none", "rolling_out"]

DEBUG_ROLLOUT_EXPLANATION_STATE_VALUES: set[DebugRolloutExplanationState] = {
    "aborted",
    "complete",
    "held",
    "none",
    "rolling_out",
}


def check_debug_rollout_explanation_state(value: str) -> DebugRolloutExplanationState:
    if value in DEBUG_ROLLOUT_EXPLANATION_STATE_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {DEBUG_ROLLOUT_EXPLANATION_STATE_VALUES!r}")
