from typing import Literal

CreateSupportTicketRequestCategory = Literal["account", "billing", "deployment", "incident", "other"]

CREATE_SUPPORT_TICKET_REQUEST_CATEGORY_VALUES: set[CreateSupportTicketRequestCategory] = {
    "account",
    "billing",
    "deployment",
    "incident",
    "other",
}


def check_create_support_ticket_request_category(value: str) -> CreateSupportTicketRequestCategory:
    if value in CREATE_SUPPORT_TICKET_REQUEST_CATEGORY_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {CREATE_SUPPORT_TICKET_REQUEST_CATEGORY_VALUES!r}")
