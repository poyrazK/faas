from typing import Literal

ObsRuntimeHTTPCronDeliveryName = Literal["app_error", "dead_letter", "failed", "retry", "success"]

OBS_RUNTIME_HTTP_CRON_DELIVERY_NAME_VALUES: set[ObsRuntimeHTTPCronDeliveryName] = {
    "app_error",
    "dead_letter",
    "failed",
    "retry",
    "success",
}


def check_obs_runtime_http_cron_delivery_name(value: str) -> ObsRuntimeHTTPCronDeliveryName:
    if value in OBS_RUNTIME_HTTP_CRON_DELIVERY_NAME_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {OBS_RUNTIME_HTTP_CRON_DELIVERY_NAME_VALUES!r}")
