from typing import Literal

AppSecretResponseLastDeliveryErrorCode = Literal["runtime_start_failed"]

APP_SECRET_RESPONSE_LAST_DELIVERY_ERROR_CODE_VALUES: set[AppSecretResponseLastDeliveryErrorCode] = {
    "runtime_start_failed",
}


def check_app_secret_response_last_delivery_error_code(value: str) -> AppSecretResponseLastDeliveryErrorCode:
    if value in APP_SECRET_RESPONSE_LAST_DELIVERY_ERROR_CODE_VALUES:
        return value
    raise TypeError(
        f"Unexpected value {value!r}. Expected one of {APP_SECRET_RESPONSE_LAST_DELIVERY_ERROR_CODE_VALUES!r}"
    )
