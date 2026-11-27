from typing import Literal

AlertAnomalyPreviewResponseStepSeconds = Literal[60, 3600]

ALERT_ANOMALY_PREVIEW_RESPONSE_STEP_SECONDS_VALUES: set[AlertAnomalyPreviewResponseStepSeconds] = {
    60,
    3600,
}


def check_alert_anomaly_preview_response_step_seconds(value: int) -> AlertAnomalyPreviewResponseStepSeconds:
    if value in ALERT_ANOMALY_PREVIEW_RESPONSE_STEP_SECONDS_VALUES:
        return value
    raise TypeError(
        f"Unexpected value {value!r}. Expected one of {ALERT_ANOMALY_PREVIEW_RESPONSE_STEP_SECONDS_VALUES!r}"
    )
