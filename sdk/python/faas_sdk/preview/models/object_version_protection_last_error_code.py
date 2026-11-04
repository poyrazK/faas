from typing import Literal

ObjectVersionProtectionLastErrorCode = Literal[
    "preparation_failed", "provider_mismatch", "provider_rejected", "provider_uncertain", "provider_unsupported"
]

OBJECT_VERSION_PROTECTION_LAST_ERROR_CODE_VALUES: set[ObjectVersionProtectionLastErrorCode] = {
    "preparation_failed",
    "provider_mismatch",
    "provider_rejected",
    "provider_uncertain",
    "provider_unsupported",
}


def check_object_version_protection_last_error_code(value: str) -> ObjectVersionProtectionLastErrorCode:
    if value in OBJECT_VERSION_PROTECTION_LAST_ERROR_CODE_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {OBJECT_VERSION_PROTECTION_LAST_ERROR_CODE_VALUES!r}")
