from typing import Literal

ObsAlertTriageHistoryEntryAction = Literal["acknowledge", "claim", "release", "snooze", "unacknowledge", "unsnooze"]

OBS_ALERT_TRIAGE_HISTORY_ENTRY_ACTION_VALUES: set[ObsAlertTriageHistoryEntryAction] = {
    "acknowledge",
    "claim",
    "release",
    "snooze",
    "unacknowledge",
    "unsnooze",
}


def check_obs_alert_triage_history_entry_action(value: str) -> ObsAlertTriageHistoryEntryAction:
    if value in OBS_ALERT_TRIAGE_HISTORY_ENTRY_ACTION_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {OBS_ALERT_TRIAGE_HISTORY_ENTRY_ACTION_VALUES!r}")
