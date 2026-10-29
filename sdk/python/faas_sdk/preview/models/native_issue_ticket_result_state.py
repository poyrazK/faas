from typing import Literal

NativeIssueTicketResultState = Literal["created", "linked"]

NATIVE_ISSUE_TICKET_RESULT_STATE_VALUES: set[NativeIssueTicketResultState] = {
    "created",
    "linked",
}


def check_native_issue_ticket_result_state(value: str) -> NativeIssueTicketResultState:
    if value in NATIVE_ISSUE_TICKET_RESULT_STATE_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {NATIVE_ISSUE_TICKET_RESULT_STATE_VALUES!r}")
