from typing import Literal

ProfilePeriodicObservationStatus = Literal[
    "baseline_expired", "baseline_pinned", "inconclusive", "no_regression_detected", "regressed"
]

PROFILE_PERIODIC_OBSERVATION_STATUS_VALUES: set[ProfilePeriodicObservationStatus] = {
    "baseline_expired",
    "baseline_pinned",
    "inconclusive",
    "no_regression_detected",
    "regressed",
}


def check_profile_periodic_observation_status(value: str) -> ProfilePeriodicObservationStatus:
    if value in PROFILE_PERIODIC_OBSERVATION_STATUS_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {PROFILE_PERIODIC_OBSERVATION_STATUS_VALUES!r}")
