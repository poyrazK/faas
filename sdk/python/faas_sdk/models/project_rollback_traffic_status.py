from typing import Literal

ProjectRollbackTrafficStatus = Literal[
    "awaiting_cutover", "awaiting_gateways", "awaiting_traffic", "expired", "superseded", "unavailable", "verified"
]

PROJECT_ROLLBACK_TRAFFIC_STATUS_VALUES: set[ProjectRollbackTrafficStatus] = {
    "awaiting_cutover",
    "awaiting_gateways",
    "awaiting_traffic",
    "expired",
    "superseded",
    "unavailable",
    "verified",
}


def check_project_rollback_traffic_status(value: str) -> ProjectRollbackTrafficStatus:
    if value in PROJECT_ROLLBACK_TRAFFIC_STATUS_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {PROJECT_ROLLBACK_TRAFFIC_STATUS_VALUES!r}")
