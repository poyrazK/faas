from typing import Literal

ProfilePeriodicObservationTransition = Literal["profile.route_recovered", "profile.route_regressed"]

PROFILE_PERIODIC_OBSERVATION_TRANSITION_VALUES: set[ProfilePeriodicObservationTransition] = {
    "profile.route_recovered",
    "profile.route_regressed",
}


def check_profile_periodic_observation_transition(value: str) -> ProfilePeriodicObservationTransition:
    if value in PROFILE_PERIODIC_OBSERVATION_TRANSITION_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {PROFILE_PERIODIC_OBSERVATION_TRANSITION_VALUES!r}")
