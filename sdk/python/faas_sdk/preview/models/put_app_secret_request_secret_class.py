from typing import Literal

PutAppSecretRequestSecretClass = Literal["ephemeral", "persistent"]

PUT_APP_SECRET_REQUEST_SECRET_CLASS_VALUES: set[PutAppSecretRequestSecretClass] = {
    "ephemeral",
    "persistent",
}


def check_put_app_secret_request_secret_class(value: str) -> PutAppSecretRequestSecretClass:
    if value in PUT_APP_SECRET_REQUEST_SECRET_CLASS_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {PUT_APP_SECRET_REQUEST_SECRET_CLASS_VALUES!r}")
