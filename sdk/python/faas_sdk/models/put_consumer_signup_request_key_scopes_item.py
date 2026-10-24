from typing import Literal

PutConsumerSignupRequestKeyScopesItem = Literal["admin", "billing", "read", "write"]

PUT_CONSUMER_SIGNUP_REQUEST_KEY_SCOPES_ITEM_VALUES: set[PutConsumerSignupRequestKeyScopesItem] = {
    "admin",
    "billing",
    "read",
    "write",
}


def check_put_consumer_signup_request_key_scopes_item(value: str) -> PutConsumerSignupRequestKeyScopesItem:
    if value in PUT_CONSUMER_SIGNUP_REQUEST_KEY_SCOPES_ITEM_VALUES:
        return value
    raise TypeError(
        f"Unexpected value {value!r}. Expected one of {PUT_CONSUMER_SIGNUP_REQUEST_KEY_SCOPES_ITEM_VALUES!r}"
    )
