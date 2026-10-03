from typing import Literal

SecretRevocationTargetErrorCode = Literal["application_reload_failed"]

SECRET_REVOCATION_TARGET_ERROR_CODE_VALUES: set[SecretRevocationTargetErrorCode] = {
    "application_reload_failed",
}


def check_secret_revocation_target_error_code(value: str) -> SecretRevocationTargetErrorCode:
    if value in SECRET_REVOCATION_TARGET_ERROR_CODE_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {SECRET_REVOCATION_TARGET_ERROR_CODE_VALUES!r}")
