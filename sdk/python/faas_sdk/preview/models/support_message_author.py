from typing import Literal

SupportMessageAuthor = Literal["customer", "staff"]

SUPPORT_MESSAGE_AUTHOR_VALUES: set[SupportMessageAuthor] = {
    "customer",
    "staff",
}


def check_support_message_author(value: str) -> SupportMessageAuthor:
    if value in SUPPORT_MESSAGE_AUTHOR_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {SUPPORT_MESSAGE_AUTHOR_VALUES!r}")
