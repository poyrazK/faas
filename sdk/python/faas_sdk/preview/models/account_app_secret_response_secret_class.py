from typing import Literal

AccountAppSecretResponseSecretClass = Literal["ephemeral", "persistent"]

ACCOUNT_APP_SECRET_RESPONSE_SECRET_CLASS_VALUES: set[AccountAppSecretResponseSecretClass] = {
    "ephemeral",
    "persistent",
}


def check_account_app_secret_response_secret_class(value: str) -> AccountAppSecretResponseSecretClass:
    if value in ACCOUNT_APP_SECRET_RESPONSE_SECRET_CLASS_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {ACCOUNT_APP_SECRET_RESPONSE_SECRET_CLASS_VALUES!r}")
