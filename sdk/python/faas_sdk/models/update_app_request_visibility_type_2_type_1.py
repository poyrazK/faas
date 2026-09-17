from typing import Literal

UpdateAppRequestVisibilityType2Type1 = Literal["internal", "public"]

UPDATE_APP_REQUEST_VISIBILITY_TYPE_2_TYPE_1_VALUES: set[UpdateAppRequestVisibilityType2Type1] = {
    "internal",
    "public",
}


def check_update_app_request_visibility_type_2_type_1(value: str) -> UpdateAppRequestVisibilityType2Type1:
    if value in UPDATE_APP_REQUEST_VISIBILITY_TYPE_2_TYPE_1_VALUES:
        return value
    raise TypeError(
        f"Unexpected value {value!r}. Expected one of {UPDATE_APP_REQUEST_VISIBILITY_TYPE_2_TYPE_1_VALUES!r}"
    )
