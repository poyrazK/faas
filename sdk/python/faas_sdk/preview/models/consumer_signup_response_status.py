from typing import Literal

ConsumerSignupResponseStatus = Literal["completed", "pending"]

CONSUMER_SIGNUP_RESPONSE_STATUS_VALUES: set[ConsumerSignupResponseStatus] = {
    "completed",
    "pending",
}


def check_consumer_signup_response_status(value: str) -> ConsumerSignupResponseStatus:
    if value in CONSUMER_SIGNUP_RESPONSE_STATUS_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {CONSUMER_SIGNUP_RESPONSE_STATUS_VALUES!r}")
