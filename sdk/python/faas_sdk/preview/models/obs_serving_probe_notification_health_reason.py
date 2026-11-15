from typing import Literal

ObsServingProbeNotificationHealthReason = Literal[
    "incomplete_evidence",
    "inventory_not_configured",
    "inventory_unavailable",
    "metadata_inconsistent",
    "prometheus_not_configured",
    "zero_expected_probes",
]

OBS_SERVING_PROBE_NOTIFICATION_HEALTH_REASON_VALUES: set[ObsServingProbeNotificationHealthReason] = {
    "incomplete_evidence",
    "inventory_not_configured",
    "inventory_unavailable",
    "metadata_inconsistent",
    "prometheus_not_configured",
    "zero_expected_probes",
}


def check_obs_serving_probe_notification_health_reason(value: str) -> ObsServingProbeNotificationHealthReason:
    if value in OBS_SERVING_PROBE_NOTIFICATION_HEALTH_REASON_VALUES:
        return value
    raise TypeError(
        f"Unexpected value {value!r}. Expected one of {OBS_SERVING_PROBE_NOTIFICATION_HEALTH_REASON_VALUES!r}"
    )
