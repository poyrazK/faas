from typing import Literal

AlertRollbackRollbackPhase = Literal["blocked", "complete", "failed", "preparing", "ready", "routing"]

ALERT_ROLLBACK_ROLLBACK_PHASE_VALUES: set[AlertRollbackRollbackPhase] = {
    "blocked",
    "complete",
    "failed",
    "preparing",
    "ready",
    "routing",
}


def check_alert_rollback_rollback_phase(value: str) -> AlertRollbackRollbackPhase:
    if value in ALERT_ROLLBACK_ROLLBACK_PHASE_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {ALERT_ROLLBACK_ROLLBACK_PHASE_VALUES!r}")
