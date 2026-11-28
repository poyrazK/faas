from typing import Literal

ObsAlertEpisodeObservationState = Literal["current", "historical", "unknown"]

OBS_ALERT_EPISODE_OBSERVATION_STATE_VALUES: set[ObsAlertEpisodeObservationState] = {
    "current",
    "historical",
    "unknown",
}


def check_obs_alert_episode_observation_state(value: str) -> ObsAlertEpisodeObservationState:
    if value in OBS_ALERT_EPISODE_OBSERVATION_STATE_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {OBS_ALERT_EPISODE_OBSERVATION_STATE_VALUES!r}")
