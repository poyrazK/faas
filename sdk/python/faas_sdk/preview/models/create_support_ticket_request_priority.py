from typing import Literal

CreateSupportTicketRequestPriority = Literal["high", "low", "normal", "urgent"]

CREATE_SUPPORT_TICKET_REQUEST_PRIORITY_VALUES: set[CreateSupportTicketRequestPriority] = {
    "high",
    "low",
    "normal",
    "urgent",
}


def check_create_support_ticket_request_priority(value: str) -> CreateSupportTicketRequestPriority:
    if value in CREATE_SUPPORT_TICKET_REQUEST_PRIORITY_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {CREATE_SUPPORT_TICKET_REQUEST_PRIORITY_VALUES!r}")
