from typing import Literal

AppSecretResponseSecretClass = Literal["ephemeral", "persistent"]

APP_SECRET_RESPONSE_SECRET_CLASS_VALUES: set[AppSecretResponseSecretClass] = {
    "ephemeral",
    "persistent",
}


def check_app_secret_response_secret_class(value: str) -> AppSecretResponseSecretClass:
    if value in APP_SECRET_RESPONSE_SECRET_CLASS_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {APP_SECRET_RESPONSE_SECRET_CLASS_VALUES!r}")
