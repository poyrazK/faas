from typing import Literal

GlobalImageReleaseTargetResponseDiagnostic = Literal[
    "", "identity_changed", "receipt_invalid", "regional_rejected", "regional_unavailable"
]

GLOBAL_IMAGE_RELEASE_TARGET_RESPONSE_DIAGNOSTIC_VALUES: set[GlobalImageReleaseTargetResponseDiagnostic] = {
    "",
    "identity_changed",
    "receipt_invalid",
    "regional_rejected",
    "regional_unavailable",
}


def check_global_image_release_target_response_diagnostic(value: str) -> GlobalImageReleaseTargetResponseDiagnostic:
    if value in GLOBAL_IMAGE_RELEASE_TARGET_RESPONSE_DIAGNOSTIC_VALUES:
        return value
    raise TypeError(
        f"Unexpected value {value!r}. Expected one of {GLOBAL_IMAGE_RELEASE_TARGET_RESPONSE_DIAGNOSTIC_VALUES!r}"
    )
