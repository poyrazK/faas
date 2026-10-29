from typing import Literal

PublicStatusIndicatorId = Literal["api_availability", "build_success", "wake_p95"]

PUBLIC_STATUS_INDICATOR_ID_VALUES: set[PublicStatusIndicatorId] = {
    "api_availability",
    "build_success",
    "wake_p95",
}


def check_public_status_indicator_id(value: str) -> PublicStatusIndicatorId:
    if value in PUBLIC_STATUS_INDICATOR_ID_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {PUBLIC_STATUS_INDICATOR_ID_VALUES!r}")
