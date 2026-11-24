from typing import Literal

UpdateAppRequestDeclaredAccessType1 = Literal["", "key", "public"]

UPDATE_APP_REQUEST_DECLARED_ACCESS_TYPE_1_VALUES: set[UpdateAppRequestDeclaredAccessType1] = {
    "",
    "key",
    "public",
}


def check_update_app_request_declared_access_type_1(value: str) -> UpdateAppRequestDeclaredAccessType1:
    if value in UPDATE_APP_REQUEST_DECLARED_ACCESS_TYPE_1_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {UPDATE_APP_REQUEST_DECLARED_ACCESS_TYPE_1_VALUES!r}")
