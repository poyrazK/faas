from typing import Literal

UpdateAppRequestConsumerAuthModeType2Type1 = Literal["optional", "required"]

UPDATE_APP_REQUEST_CONSUMER_AUTH_MODE_TYPE_2_TYPE_1_VALUES: set[UpdateAppRequestConsumerAuthModeType2Type1] = {
    "optional",
    "required",
}


def check_update_app_request_consumer_auth_mode_type_2_type_1(value: str) -> UpdateAppRequestConsumerAuthModeType2Type1:
    if value in UPDATE_APP_REQUEST_CONSUMER_AUTH_MODE_TYPE_2_TYPE_1_VALUES:
        return value
    raise TypeError(
        f"Unexpected value {value!r}. Expected one of {UPDATE_APP_REQUEST_CONSUMER_AUTH_MODE_TYPE_2_TYPE_1_VALUES!r}"
    )
