from typing import Literal

ProfileInvestigationWindowStatusStatus = Literal[
    "backend_unavailable", "deployment_unavailable", "expired", "plan_unavailable", "retained"
]

PROFILE_INVESTIGATION_WINDOW_STATUS_STATUS_VALUES: set[ProfileInvestigationWindowStatusStatus] = {
    "backend_unavailable",
    "deployment_unavailable",
    "expired",
    "plan_unavailable",
    "retained",
}


def check_profile_investigation_window_status_status(value: str) -> ProfileInvestigationWindowStatusStatus:
    if value in PROFILE_INVESTIGATION_WINDOW_STATUS_STATUS_VALUES:
        return value
    raise TypeError(
        f"Unexpected value {value!r}. Expected one of {PROFILE_INVESTIGATION_WINDOW_STATUS_STATUS_VALUES!r}"
    )
