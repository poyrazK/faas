from typing import Literal

DeleteSecretPrefer = Literal["return=representation"]

DELETE_SECRET_PREFER_VALUES: set[DeleteSecretPrefer] = {
    "return=representation",
}


def check_delete_secret_prefer(value: str) -> DeleteSecretPrefer:
    if value in DELETE_SECRET_PREFER_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {DELETE_SECRET_PREFER_VALUES!r}")
