from typing import Literal

WorkflowRecoveryOverviewMonitoringStatus = Literal["failing", "no_observed_failure", "unknown"]

WORKFLOW_RECOVERY_OVERVIEW_MONITORING_STATUS_VALUES: set[WorkflowRecoveryOverviewMonitoringStatus] = {
    "failing",
    "no_observed_failure",
    "unknown",
}


def check_workflow_recovery_overview_monitoring_status(value: str) -> WorkflowRecoveryOverviewMonitoringStatus:
    if value in WORKFLOW_RECOVERY_OVERVIEW_MONITORING_STATUS_VALUES:
        return value
    raise TypeError(
        f"Unexpected value {value!r}. Expected one of {WORKFLOW_RECOVERY_OVERVIEW_MONITORING_STATUS_VALUES!r}"
    )
