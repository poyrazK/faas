from typing import Literal

ObsInputMetricMetric = Literal[
    "apid_daemon_ready",
    "gatewayd_daemon_ready",
    "gatewayd_public_daemon_ready",
    "schedd_daemon_ready",
    "vmmd_daemon_ready",
]

OBS_INPUT_METRIC_METRIC_VALUES: set[ObsInputMetricMetric] = {
    "apid_daemon_ready",
    "gatewayd_daemon_ready",
    "gatewayd_public_daemon_ready",
    "schedd_daemon_ready",
    "vmmd_daemon_ready",
}


def check_obs_input_metric_metric(value: str) -> ObsInputMetricMetric:
    if value in OBS_INPUT_METRIC_METRIC_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {OBS_INPUT_METRIC_METRIC_VALUES!r}")
