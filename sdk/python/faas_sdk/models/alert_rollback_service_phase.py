from typing import Literal

AlertRollbackServicePhase = Literal["complete", "draining", "pending", "routing"]

ALERT_ROLLBACK_SERVICE_PHASE_VALUES: set[AlertRollbackServicePhase] = {
    "complete",
    "draining",
    "pending",
    "routing",
}


def check_alert_rollback_service_phase(value: str) -> AlertRollbackServicePhase:
    if value in ALERT_ROLLBACK_SERVICE_PHASE_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {ALERT_ROLLBACK_SERVICE_PHASE_VALUES!r}")
