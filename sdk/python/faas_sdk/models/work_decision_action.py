from typing import Literal

WorkDecisionAction = Literal["complete", "fail_partition", "hold", "retry"]

WORK_DECISION_ACTION_VALUES: set[WorkDecisionAction] = {
    "complete",
    "fail_partition",
    "hold",
    "retry",
}


def check_work_decision_action(value: str) -> WorkDecisionAction:
    if value in WORK_DECISION_ACTION_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {WORK_DECISION_ACTION_VALUES!r}")
