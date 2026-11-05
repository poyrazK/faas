from typing import Literal

RolloutDecisionKind = Literal["abort", "advance", "hold"]

ROLLOUT_DECISION_KIND_VALUES: set[RolloutDecisionKind] = {
    "abort",
    "advance",
    "hold",
}


def check_rollout_decision_kind(value: str) -> RolloutDecisionKind:
    if value in ROLLOUT_DECISION_KIND_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {ROLLOUT_DECISION_KIND_VALUES!r}")
