from typing import Literal

GlobalRuntimeQuotaResponseStatus = Literal["reserved", "retired", "retiring"]

GLOBAL_RUNTIME_QUOTA_RESPONSE_STATUS_VALUES: set[GlobalRuntimeQuotaResponseStatus] = {
    "reserved",
    "retired",
    "retiring",
}


def check_global_runtime_quota_response_status(value: str) -> GlobalRuntimeQuotaResponseStatus:
    if value in GLOBAL_RUNTIME_QUOTA_RESPONSE_STATUS_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {GLOBAL_RUNTIME_QUOTA_RESPONSE_STATUS_VALUES!r}")
