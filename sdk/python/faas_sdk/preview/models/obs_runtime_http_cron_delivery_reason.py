from typing import Literal

ObsRuntimeHTTPCronDeliveryReason = Literal[
    "comparison_not_requested",
    "http_cron_delivery_evidence_unavailable",
    "http_cron_delivery_increases_observed",
    "incomplete_http_cron_delivery_evidence",
]

OBS_RUNTIME_HTTP_CRON_DELIVERY_REASON_VALUES: set[ObsRuntimeHTTPCronDeliveryReason] = {
    "comparison_not_requested",
    "http_cron_delivery_evidence_unavailable",
    "http_cron_delivery_increases_observed",
    "incomplete_http_cron_delivery_evidence",
}


def check_obs_runtime_http_cron_delivery_reason(value: str) -> ObsRuntimeHTTPCronDeliveryReason:
    if value in OBS_RUNTIME_HTTP_CRON_DELIVERY_REASON_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {OBS_RUNTIME_HTTP_CRON_DELIVERY_REASON_VALUES!r}")
