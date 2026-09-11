from typing import Literal

UpdateAppRequestConsumerAuthModeType3Type1 = Literal["optional", "required"]

UPDATE_APP_REQUEST_CONSUMER_AUTH_MODE_TYPE_3_TYPE_1_VALUES: set[UpdateAppRequestConsumerAuthModeType3Type1] = {
    "optional",
    "required",
}


def check_update_app_request_consumer_auth_mode_type_3_type_1(value: str) -> UpdateAppRequestConsumerAuthModeType3Type1:
    if value in UPDATE_APP_REQUEST_CONSUMER_AUTH_MODE_TYPE_3_TYPE_1_VALUES:
        return value
    raise TypeError(
        f"Unexpected value {value!r}. Expected one of {UPDATE_APP_REQUEST_CONSUMER_AUTH_MODE_TYPE_3_TYPE_1_VALUES!r}"
    )
