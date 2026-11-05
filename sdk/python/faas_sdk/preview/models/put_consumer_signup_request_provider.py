from typing import Literal

PutConsumerSignupRequestProvider = Literal["stripe"]

PUT_CONSUMER_SIGNUP_REQUEST_PROVIDER_VALUES: set[PutConsumerSignupRequestProvider] = {
    "stripe",
}


def check_put_consumer_signup_request_provider(value: str) -> PutConsumerSignupRequestProvider:
    if value in PUT_CONSUMER_SIGNUP_REQUEST_PROVIDER_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {PUT_CONSUMER_SIGNUP_REQUEST_PROVIDER_VALUES!r}")
