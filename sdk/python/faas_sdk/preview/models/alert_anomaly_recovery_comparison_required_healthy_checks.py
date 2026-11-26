from typing import Literal

AlertAnomalyRecoveryComparisonRequiredHealthyChecks = Literal[2]

ALERT_ANOMALY_RECOVERY_COMPARISON_REQUIRED_HEALTHY_CHECKS_VALUES: set[
    AlertAnomalyRecoveryComparisonRequiredHealthyChecks
] = {
    2,
}


def check_alert_anomaly_recovery_comparison_required_healthy_checks(
    value: int,
) -> AlertAnomalyRecoveryComparisonRequiredHealthyChecks:
    if value in ALERT_ANOMALY_RECOVERY_COMPARISON_REQUIRED_HEALTHY_CHECKS_VALUES:
        return value
    raise TypeError(
        f"Unexpected value {value!r}. Expected one of {ALERT_ANOMALY_RECOVERY_COMPARISON_REQUIRED_HEALTHY_CHECKS_VALUES!r}"
    )
