from typing import Literal

SupportTicketStatus = Literal["open", "pending", "resolved"]

SUPPORT_TICKET_STATUS_VALUES: set[SupportTicketStatus] = {
    "open",
    "pending",
    "resolved",
}


def check_support_ticket_status(value: str) -> SupportTicketStatus:
    if value in SUPPORT_TICKET_STATUS_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {SUPPORT_TICKET_STATUS_VALUES!r}")
