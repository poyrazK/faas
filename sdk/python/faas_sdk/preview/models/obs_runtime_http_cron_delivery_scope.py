from typing import Literal

ObsRuntimeHTTPCronDeliveryScope = Literal["process_local_counter_observations"]

OBS_RUNTIME_HTTP_CRON_DELIVERY_SCOPE_VALUES: set[ObsRuntimeHTTPCronDeliveryScope] = {
    "process_local_counter_observations",
}


def check_obs_runtime_http_cron_delivery_scope(value: str) -> ObsRuntimeHTTPCronDeliveryScope:
    if value in OBS_RUNTIME_HTTP_CRON_DELIVERY_SCOPE_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {OBS_RUNTIME_HTTP_CRON_DELIVERY_SCOPE_VALUES!r}")
