from typing import Literal

UpdateAppRequestDeclaredAccessType3Type1 = Literal["", "key", "public"]

UPDATE_APP_REQUEST_DECLARED_ACCESS_TYPE_3_TYPE_1_VALUES: set[UpdateAppRequestDeclaredAccessType3Type1] = {
    "",
    "key",
    "public",
}


def check_update_app_request_declared_access_type_3_type_1(value: str) -> UpdateAppRequestDeclaredAccessType3Type1:
    if value in UPDATE_APP_REQUEST_DECLARED_ACCESS_TYPE_3_TYPE_1_VALUES:
        return value
    raise TypeError(
        f"Unexpected value {value!r}. Expected one of {UPDATE_APP_REQUEST_DECLARED_ACCESS_TYPE_3_TYPE_1_VALUES!r}"
    )
