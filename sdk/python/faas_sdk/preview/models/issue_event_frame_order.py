from typing import Literal

IssueEventFrameOrder = Literal["newest-first", "oldest-first"]

ISSUE_EVENT_FRAME_ORDER_VALUES: set[IssueEventFrameOrder] = {
    "newest-first",
    "oldest-first",
}


def check_issue_event_frame_order(value: str) -> IssueEventFrameOrder:
    if value in ISSUE_EVENT_FRAME_ORDER_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {ISSUE_EVENT_FRAME_ORDER_VALUES!r}")
