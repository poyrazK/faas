from typing import Literal

IssueCauseFrameOrder = Literal["newest-first", "oldest-first"]

ISSUE_CAUSE_FRAME_ORDER_VALUES: set[IssueCauseFrameOrder] = {
    "newest-first",
    "oldest-first",
}


def check_issue_cause_frame_order(value: str) -> IssueCauseFrameOrder:
    if value in ISSUE_CAUSE_FRAME_ORDER_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {ISSUE_CAUSE_FRAME_ORDER_VALUES!r}")
