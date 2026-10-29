from typing import Literal

ScopedAppSecretResponseLastDeliveryErrorCode = Literal["runtime_start_failed"]

SCOPED_APP_SECRET_RESPONSE_LAST_DELIVERY_ERROR_CODE_VALUES: set[ScopedAppSecretResponseLastDeliveryErrorCode] = {
    "runtime_start_failed",
}


def check_scoped_app_secret_response_last_delivery_error_code(
    value: str,
) -> ScopedAppSecretResponseLastDeliveryErrorCode:
    if value in SCOPED_APP_SECRET_RESPONSE_LAST_DELIVERY_ERROR_CODE_VALUES:
        return value
    raise TypeError(
        f"Unexpected value {value!r}. Expected one of {SCOPED_APP_SECRET_RESPONSE_LAST_DELIVERY_ERROR_CODE_VALUES!r}"
    )
