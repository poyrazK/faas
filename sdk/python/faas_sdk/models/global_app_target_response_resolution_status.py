from typing import Literal

GlobalAppTargetResponseResolutionStatus = Literal["cell_disabled", "reverify_required", "verified"]

GLOBAL_APP_TARGET_RESPONSE_RESOLUTION_STATUS_VALUES: set[GlobalAppTargetResponseResolutionStatus] = {
    "cell_disabled",
    "reverify_required",
    "verified",
}


def check_global_app_target_response_resolution_status(value: str) -> GlobalAppTargetResponseResolutionStatus:
    if value in GLOBAL_APP_TARGET_RESPONSE_RESOLUTION_STATUS_VALUES:
        return value
    raise TypeError(
        f"Unexpected value {value!r}. Expected one of {GLOBAL_APP_TARGET_RESPONSE_RESOLUTION_STATUS_VALUES!r}"
    )
