from typing import Literal

SecretRevocationTargetReloadSupport = Literal["disabled", "enabled", "unknown"]

SECRET_REVOCATION_TARGET_RELOAD_SUPPORT_VALUES: set[SecretRevocationTargetReloadSupport] = {
    "disabled",
    "enabled",
    "unknown",
}


def check_secret_revocation_target_reload_support(value: str) -> SecretRevocationTargetReloadSupport:
    if value in SECRET_REVOCATION_TARGET_RELOAD_SUPPORT_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {SECRET_REVOCATION_TARGET_RELOAD_SUPPORT_VALUES!r}")
