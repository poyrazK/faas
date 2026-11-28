from typing import Literal

ObsRuntimeHTTPCronDeliveryItemJob = Literal["schedd", "schedd-compute"]

OBS_RUNTIME_HTTP_CRON_DELIVERY_ITEM_JOB_VALUES: set[ObsRuntimeHTTPCronDeliveryItemJob] = {
    "schedd",
    "schedd-compute",
}


def check_obs_runtime_http_cron_delivery_item_job(value: str) -> ObsRuntimeHTTPCronDeliveryItemJob:
    if value in OBS_RUNTIME_HTTP_CRON_DELIVERY_ITEM_JOB_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {OBS_RUNTIME_HTTP_CRON_DELIVERY_ITEM_JOB_VALUES!r}")
