from typing import Literal

ObsRuntimeHTTPCronDeliverySignalState = Literal[
    "baseline", "conflicting", "increased", "missing", "stale", "unavailable", "unchanged"
]

OBS_RUNTIME_HTTP_CRON_DELIVERY_SIGNAL_STATE_VALUES: set[ObsRuntimeHTTPCronDeliverySignalState] = {
    "baseline",
    "conflicting",
    "increased",
    "missing",
    "stale",
    "unavailable",
    "unchanged",
}


def check_obs_runtime_http_cron_delivery_signal_state(value: str) -> ObsRuntimeHTTPCronDeliverySignalState:
    if value in OBS_RUNTIME_HTTP_CRON_DELIVERY_SIGNAL_STATE_VALUES:
        return value
    raise TypeError(
        f"Unexpected value {value!r}. Expected one of {OBS_RUNTIME_HTTP_CRON_DELIVERY_SIGNAL_STATE_VALUES!r}"
    )
