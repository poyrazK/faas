from typing import Literal

AppResponseConsumerAuthMode = Literal["optional", "required"]

APP_RESPONSE_CONSUMER_AUTH_MODE_VALUES: set[AppResponseConsumerAuthMode] = {
    "optional",
    "required",
}


def check_app_response_consumer_auth_mode(value: str) -> AppResponseConsumerAuthMode:
    if value in APP_RESPONSE_CONSUMER_AUTH_MODE_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {APP_RESPONSE_CONSUMER_AUTH_MODE_VALUES!r}")
