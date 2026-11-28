from typing import Literal

ObsRuntimeHTTPCronDeliveryState = Literal["baseline", "incomplete", "increased", "unchanged", "unknown"]

OBS_RUNTIME_HTTP_CRON_DELIVERY_STATE_VALUES: set[ObsRuntimeHTTPCronDeliveryState] = {
    "baseline",
    "incomplete",
    "increased",
    "unchanged",
    "unknown",
}


def check_obs_runtime_http_cron_delivery_state(value: str) -> ObsRuntimeHTTPCronDeliveryState:
    if value in OBS_RUNTIME_HTTP_CRON_DELIVERY_STATE_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {OBS_RUNTIME_HTTP_CRON_DELIVERY_STATE_VALUES!r}")
