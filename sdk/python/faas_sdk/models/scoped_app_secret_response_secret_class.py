from typing import Literal

ScopedAppSecretResponseSecretClass = Literal["ephemeral", "persistent"]

SCOPED_APP_SECRET_RESPONSE_SECRET_CLASS_VALUES: set[ScopedAppSecretResponseSecretClass] = {
    "ephemeral",
    "persistent",
}


def check_scoped_app_secret_response_secret_class(value: str) -> ScopedAppSecretResponseSecretClass:
    if value in SCOPED_APP_SECRET_RESPONSE_SECRET_CLASS_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {SCOPED_APP_SECRET_RESPONSE_SECRET_CLASS_VALUES!r}")
