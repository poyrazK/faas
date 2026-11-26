from typing import Literal

PreviewAlertAnomalyRecoveryConfirmationChecks = Literal[2]

PREVIEW_ALERT_ANOMALY_RECOVERY_CONFIRMATION_CHECKS_VALUES: set[PreviewAlertAnomalyRecoveryConfirmationChecks] = {
    2,
}


def check_preview_alert_anomaly_recovery_confirmation_checks(
    value: int,
) -> PreviewAlertAnomalyRecoveryConfirmationChecks:
    if value in PREVIEW_ALERT_ANOMALY_RECOVERY_CONFIRMATION_CHECKS_VALUES:
        return value
    raise TypeError(
        f"Unexpected value {value!r}. Expected one of {PREVIEW_ALERT_ANOMALY_RECOVERY_CONFIRMATION_CHECKS_VALUES!r}"
    )
