from typing import Literal

UpdateAppRequestVisibilityType1 = Literal["internal", "public"]

UPDATE_APP_REQUEST_VISIBILITY_TYPE_1_VALUES: set[UpdateAppRequestVisibilityType1] = {
    "internal",
    "public",
}


def check_update_app_request_visibility_type_1(value: str) -> UpdateAppRequestVisibilityType1:
    if value in UPDATE_APP_REQUEST_VISIBILITY_TYPE_1_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {UPDATE_APP_REQUEST_VISIBILITY_TYPE_1_VALUES!r}")
