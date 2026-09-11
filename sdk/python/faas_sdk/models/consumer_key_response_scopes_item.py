from typing import Literal

ConsumerKeyResponseScopesItem = Literal["admin", "read", "write"]

CONSUMER_KEY_RESPONSE_SCOPES_ITEM_VALUES: set[ConsumerKeyResponseScopesItem] = {
    "admin",
    "read",
    "write",
}


def check_consumer_key_response_scopes_item(value: str) -> ConsumerKeyResponseScopesItem:
    if value in CONSUMER_KEY_RESPONSE_SCOPES_ITEM_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {CONSUMER_KEY_RESPONSE_SCOPES_ITEM_VALUES!r}")
