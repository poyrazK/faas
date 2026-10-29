from typing import Literal

SecretRevocationTargetStatus = Literal["applied", "failed", "pending"]

SECRET_REVOCATION_TARGET_STATUS_VALUES: set[SecretRevocationTargetStatus] = {
    "applied",
    "failed",
    "pending",
}


def check_secret_revocation_target_status(value: str) -> SecretRevocationTargetStatus:
    if value in SECRET_REVOCATION_TARGET_STATUS_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {SECRET_REVOCATION_TARGET_STATUS_VALUES!r}")
