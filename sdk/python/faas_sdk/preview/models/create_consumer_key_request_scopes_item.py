from typing import Literal

CreateConsumerKeyRequestScopesItem = Literal["admin", "billing", "read", "write"]

CREATE_CONSUMER_KEY_REQUEST_SCOPES_ITEM_VALUES: set[CreateConsumerKeyRequestScopesItem] = {
    "admin",
    "billing",
    "read",
    "write",
}


def check_create_consumer_key_request_scopes_item(value: str) -> CreateConsumerKeyRequestScopesItem:
    if value in CREATE_CONSUMER_KEY_REQUEST_SCOPES_ITEM_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {CREATE_CONSUMER_KEY_REQUEST_SCOPES_ITEM_VALUES!r}")
